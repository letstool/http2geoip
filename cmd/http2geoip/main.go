package main

import (
    _ "embed"
    "archive/tar"
    "compress/gzip"
    "context"
    "encoding/json"
    "flag"
    "fmt"
    "io"
    "log"
    "net"
    "net/http"
    "net/url"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "sync/atomic"
    "time"

    _ "github.com/breml/rootcerts" // embed Mozilla CA bundle as fallback for scratch containers

    "github.com/oschwald/geoip2-golang"
)

//go:embed static/index.html
var indexHTML []byte

//go:embed static/favicon.png
var faviconPNG []byte

//go:embed static/openapi.json
var openapiJSON []byte

/* ---------- Types ---------- */
type geoAnswer struct {
    IP              string   `json:"ip"`
    ContinentCode   *string  `json:"continent_code"`
    ContinentName   *string  `json:"continent_name"`
    CountryISOCode  *string  `json:"country_isocode"`
    CountryName     *string  `json:"country_name"`
    Accuracy        *uint16  `json:"accuracy"`
    Latitude        *float64 `json:"latitude"`
    Longitude       *float64 `json:"longitude"`
    TimeZone        *string  `json:"time_zone"`
    RegCountryName  *string  `json:"reg_country_name"`
    RegCountryCode  *string  `json:"reg_country_code"`
}

type geoResponse struct {
    Status  string      `json:"status"`
    Answers []geoAnswer `json:"answers"`
}

type geoIPRequest struct {
    IP   *string  `json:"ip"`
    IPs  []string `json:"ips"`
    Lang string   `json:"lang"`
}

/* ---------- Configuration ---------- */
var (
    maxIPs       int
    dbURL        string          // URL of a tar.gz archive or a peer
    dbDir        string          // storage directory
    updateTime   time.Time       // daily update time (HH:MM UTC)
    listenAddr   string          // listen address
    dbValue      atomic.Value    // (*geoip2.Reader)
    todayDate    string
)

const (
    lastUpdateFile = ".last_update" // file containing the date of the last DB update
    maxMindDomain  = "download.maxmind.com"
)

/* ---------- Helpers ---------- */
var allowedLangs = map[string]struct{}{
    "en":    {},
    "es":    {},
    "fr":    {},
    "ja":    {},
    "pt-BR": {},
    "ru":    {},
    "zh-CN": {},
}

func getLocalizedName(names map[string]string, lang string) *string {
    if names == nil {
        return nil
    }
    if n, ok := names[lang]; ok {
        return &n
    }
    if n, ok := names["en"]; ok {
        return &n
    }
    return nil
}

func today() string {
    return time.Now().UTC().Format("20060102")
}

/* ---------- GeoIP lookup ---------- */
func lookupIP(db *geoip2.Reader, ipStr string, lang string) (*geoAnswer, error) {
    ip := net.ParseIP(ipStr)
    if ip == nil {
        return nil, fmt.Errorf("invalid IP: %s", ipStr)
    }
    city, err := db.City(ip)
    if err != nil {
        return nil, err
    }
    if city == nil {
        return nil, nil
    }
    // No geographic data available
    if city.Continent.Code == "" &&
        city.Country.IsoCode == "" &&
        city.RegisteredCountry.IsoCode == "" &&
        city.Location.TimeZone == "" &&
        city.Location.AccuracyRadius == 0 &&
        city.Location.Latitude == 0 &&
        city.Location.Longitude == 0 {
        return nil, nil
    }
    ans := &geoAnswer{IP: ipStr}
    if city.Continent.Code != "" {
        ans.ContinentCode = &city.Continent.Code
        ans.ContinentName = getLocalizedName(city.Continent.Names, lang)
    }
    if city.Country.IsoCode != "" {
        ans.CountryISOCode = &city.Country.IsoCode
        ans.CountryName = getLocalizedName(city.Country.Names, lang)
    }
    if city.RegisteredCountry.IsoCode != "" {
        ans.RegCountryCode = &city.RegisteredCountry.IsoCode
        ans.RegCountryName = getLocalizedName(city.RegisteredCountry.Names, lang)
    }
    loc := city.Location
    if loc.TimeZone != "" || loc.AccuracyRadius != 0 || loc.Latitude != 0 || loc.Longitude != 0 {
        ans.Accuracy = &loc.AccuracyRadius
        ans.Latitude = &loc.Latitude
        ans.Longitude = &loc.Longitude
        if loc.TimeZone != "" {
            ans.TimeZone = &loc.TimeZone
        }
    }
    return ans, nil
}

/* ---------- HTTP Handlers ---------- */
func indexHandler(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/html; charset=utf-8")
        w.Write(indexHTML)
}

func faviconHandler(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "image/png")
        w.Write(faviconPNG)
}

func openapiHandler(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.Write(openapiJSON)
}

func geoIPHandler() http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost {
            http.Error(w, "Only POST allowed", http.StatusMethodNotAllowed)
            return
        }
        var req geoIPRequest
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            respondWithStatus(w, "ERROR", nil)
            return
        }
        defer r.Body.Close()

        lang := req.Lang
        if _, ok := allowedLangs[lang]; !ok || lang == "" {
            lang = "en"
        }
        if req.IPs != nil && len(req.IPs) > maxIPs {
            respondWithStatus(w, "ERROR", nil)
            return
        }
        db := dbValue.Load().(*geoip2.Reader)

        var answers []geoAnswer
        if req.IP != nil && len(req.IPs) == 0 { // single IP
            ans, err := lookupIP(db, *req.IP, lang)
            if err != nil {
                log.Printf("DB lookup error: %v", err)
                respondWithStatus(w, "ERROR", nil)
                return
            }
            if ans != nil {
                answers = append(answers, *ans)
            }
        } else if len(req.IPs) > 0 && req.IP == nil { // multiple IPs
            for _, ipStr := range req.IPs {
                ans, err := lookupIP(db, ipStr, lang)
                if err != nil {
                    log.Printf("DB lookup error for %s: %v", ipStr, err)
                    continue
                }
                if ans != nil {
                    answers = append(answers, *ans)
                }
            }
        } else {
            respondWithStatus(w, "ERROR", nil)
            return
        }

        if len(answers) == 0 {
            respondWithStatus(w, "NOTFOUND", nil)
        } else {
            respondWithStatus(w, "SUCCESS", answers)
        }
    }
}

func respondWithStatus(w http.ResponseWriter, status string, answers []geoAnswer) {
    w.Header().Set("Content-Type", "application/json")
    resp := geoResponse{Status: status, Answers: answers}
    json.NewEncoder(w).Encode(resp)
}

// Endpoint to serve the current mmdb file
func getDBHandler() http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        mmdbPath := filepath.Join(dbDir, "GeoLite2-City.mmdb")
        if _, err := os.Stat(mmdbPath); err != nil {
            http.Error(w, "File not found", http.StatusNotFound)
            return
        }
        http.ServeFile(w, r, mmdbPath)
    }
}

/* ---------- GeoIP DB Management ---------- */
func ensureDB(ctx context.Context) error {
    mmdbPath := filepath.Join(dbDir, "GeoLite2-City.mmdb")
    if _, err := os.Stat(mmdbPath); err == nil {
        last, err := os.ReadFile(filepath.Join(dbDir, lastUpdateFile))
        if err == nil && string(last) == today() {
            db, err := geoip2.Open(mmdbPath)
            if err != nil {
                return fmt.Errorf("open existing database: %w", err)
            }
            dbValue.Store(db)
            return nil
        }
    }
    if dbURL == "" {
        // No URL configured, cannot download
        return fmt.Errorf("GEOIP_DB_URL is empty, no database to load")
    }
    return updateDB(ctx)
}

// download from a peer that exposes /getdb
func downloadFromPeer(ctx context.Context) error {
    u, err := url.Parse(dbURL)
    if err != nil {
        return err
    }
    u.Path = "/getdb"
    resp, err := http.Get(u.String())
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("peer returned %s", resp.Status)
    }
    tmpMMDB, err := os.CreateTemp(dbDir, "GeoLite2-City-*.mmdb")
    if err != nil {
        return err
    }
    defer os.Remove(tmpMMDB.Name())
    if _, err := io.Copy(tmpMMDB, resp.Body); err != nil {
        return err
    }
    if err := tmpMMDB.Close(); err != nil {
        return err
    }
    newDB, err := geoip2.Open(tmpMMDB.Name())
    if err != nil {
        return err
    }
    oldDBVal := dbValue.Load()
    var oldDB *geoip2.Reader
    if oldDBVal != nil {
        oldDB = oldDBVal.(*geoip2.Reader)
    }
    dbValue.Store(newDB)
    if oldDB != nil {
        oldDB.Close()
        os.Remove(filepath.Join(dbDir, "GeoLite2-City.mmdb"))
    }
    finalPath := filepath.Join(dbDir, "GeoLite2-City.mmdb")
    if err := os.Rename(tmpMMDB.Name(), finalPath); err != nil {
        return err
    }
    if err := os.WriteFile(filepath.Join(dbDir, lastUpdateFile), []byte(today()), 0644); err != nil {
        return err
    }
    return nil
}

func updateDB(ctx context.Context) error {
    // parse the dbURL
    parsedURL, err := url.Parse(dbURL)
    if err != nil {
        return fmt.Errorf("invalid URL: %w", err)
    }

    if parsedURL.Host == maxMindDomain {
        // Classic download of tar.gz
        log.Println("Downloading GeoIP database (tar.gz)...")
        resp, err := http.Get(dbURL)
        if err != nil {
            return fmt.Errorf("download error: %w", err)
        }
        defer resp.Body.Close()
        if resp.StatusCode != http.StatusOK {
            return fmt.Errorf("download returned %s", resp.Status)
        }
        gzReader, err := gzip.NewReader(resp.Body)
        if err != nil {
            return fmt.Errorf("gzip error: %w", err)
        }
        defer gzReader.Close()
        tarReader := tar.NewReader(gzReader)

        tmpMMDB, err := os.CreateTemp(dbDir, "GeoLite2-City-*.mmdb")
        if err != nil {
            return fmt.Errorf("temp file error: %w", err)
        }
        defer os.Remove(tmpMMDB.Name())

        found := false
        for {
            header, err := tarReader.Next()
            if err == io.EOF {
                break
            }
            if err != nil {
                return fmt.Errorf("tar error: %w", err)
            }
            if header.Typeflag != tar.TypeReg {
                continue
            }
            if strings.HasSuffix(header.Name, "/GeoLite2-City.mmdb") || strings.HasSuffix(header.Name, "GeoLite2-City.mmdb") {
                if _, err := io.Copy(tmpMMDB, tarReader); err != nil {
                    return fmt.Errorf("copy error: %w", err)
                }
                found = true
                break
            }
        }
        if !found {
            return fmt.Errorf("GeoLite2-City.mmdb not found in archive")
        }
        if err := tmpMMDB.Close(); err != nil {
            return fmt.Errorf("close tmp file: %w", err)
        }

        newDB, err := geoip2.Open(tmpMMDB.Name())
        if err != nil {
            return fmt.Errorf("open new mmdb: %w", err)
        }
        oldDBVal := dbValue.Load()
        var oldDB *geoip2.Reader
        if oldDBVal != nil {
            oldDB = oldDBVal.(*geoip2.Reader)
        }
        dbValue.Store(newDB)
        if oldDB != nil {
            oldDB.Close()
            os.Remove(filepath.Join(dbDir, "GeoLite2-City.mmdb"))
        }
        finalPath := filepath.Join(dbDir, "GeoLite2-City.mmdb")
        if err := os.Rename(tmpMMDB.Name(), finalPath); err != nil {
            return fmt.Errorf("rename error: %w", err)
        }
        if err := os.WriteFile(filepath.Join(dbDir, lastUpdateFile), []byte(today()), 0644); err != nil {
            return fmt.Errorf("write .last_update: %w", err)
        }
        log.Printf("Update completed - %s", today())
        return nil
    } else if err := downloadFromPeer(ctx); err != nil {
        // On failure, retry every 5 minutes in a goroutine
        log.Printf("Peer download failed: %v. Retrying every 5 min.", err)
        go func() {
            ticker := time.NewTicker(5 * time.Minute)
            defer ticker.Stop()
            for {
                select {
                case <-ticker.C:
                    if err := downloadFromPeer(ctx); err == nil {
                        return
                    }
                case <-ctx.Done():
                    return
                }
            }
        }()
        return nil // not a critical error, will retry
    }
    log.Printf("Peer download completed - %s", today())
    return nil
}

/* ---------- Scheduler ---------- */
func scheduleDailyUpdate(ctx context.Context, targetTime time.Time) {
    if dbURL == "" {
        log.Println("GEOIP_DB_URL is empty: daily update disabled")
        return
    }
    now := time.Now().UTC()
    target := time.Date(now.Year(), now.Month(), now.Day(), targetTime.Hour(), targetTime.Minute(), 0, 0, time.UTC)
    if now.After(target) || now.Equal(target) {
        target = target.Add(24 * time.Hour)
    }
    duration := target.Sub(now)
    log.Printf("Next update scheduled in %s (UTC time %02d:%02d)", duration, targetTime.Hour(), targetTime.Minute())
    timer := time.NewTimer(duration)
    go func() {
        for {
            select {
            case <-timer.C:
                if err := updateDB(ctx); err != nil {
                    log.Printf("Scheduled update failed: %v", err)
                }
                timer.Reset(24 * time.Hour)
            case <-ctx.Done():
                timer.Stop()
                return
            }
        }
    }()
}

/* ---------- Main ---------- */
func main() {
    // Command-line flags (priority over environment variables).
    // Resolution order for each setting: flag > env var > built-in default.
    const sentinel = "\x00" // marks "flag was not provided on the command line"
    flagDBURL       := flag.String("db-url",      sentinel, "GeoIP database URL (tar.gz from MaxMind or peer base URL). Overrides GEOIP_DB_URL.")
    flagDBDir       := flag.String("db-dir",      sentinel, "Directory used to store the mmdb file. Overrides GEOIP_DB_DIR. Default: /data")
    flagListenAddr  := flag.String("listen",      sentinel, "Address and port the HTTP server listens on. Overrides GEOIP_LISTEN_ADDR. Default: 127.0.0.1:8080")
    flagMaxIPs      := flag.Int("max-ips",        -1,       "Maximum number of IPs accepted in a single request. Overrides GEOIP_MAX_IPS. Default: 100")
    flagUpdateHour  := flag.String("update-hour", sentinel, "Daily update time in HH:MM UTC (e.g. 02:00). Overrides GEOIP_UPDATE_HOUR. Default: 02:00")
    flag.Parse()

    // resolve resolves a string setting: flag wins if it was explicitly provided,
    // otherwise the env var is used, falling back to the built-in default.
    resolve := func(flagVal, envKey, defaultVal string) string {
        if flagVal != sentinel {
            return flagVal
        }
        if v := os.Getenv(envKey); v != "" {
            return v
        }
        return defaultVal
    }

    dbURL      = resolve(*flagDBURL,      "GEOIP_DB_URL",      "")
    dbDir      = resolve(*flagDBDir,      "GEOIP_DB_DIR",      "/data")
    listenAddr = resolve(*flagListenAddr, "GEOIP_LISTEN_ADDR", "127.0.0.1:8080")

    // max-ips: flag wins if >= 0, otherwise fall back to env var then default
    maxIPs = 100
    if *flagMaxIPs >= 0 {
        maxIPs = *flagMaxIPs
    } else if v := os.Getenv("GEOIP_MAX_IPS"); v != "" {
        if n, err := strconv.Atoi(v); err == nil && n > 0 {
            maxIPs = n
        }
    }

    // update-hour: parse whichever source wins
    updateHourStr := resolve(*flagUpdateHour, "GEOIP_UPDATE_HOUR", "02:00")
    t, err := time.Parse("15:04", updateHourStr)
    if err != nil {
        log.Fatalf("invalid update hour %q: %v", updateHourStr, err)
    }
    updateTime = t

    // Create directory if needed
    if err := os.MkdirAll(dbDir, 0755); err != nil {
        log.Fatalf("failed to create directory %s: %v", dbDir, err)
    }

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Load existing database or download it
    if err := ensureDB(ctx); err != nil {
        log.Fatalf("failed to initialize GeoIP database: %v", err)
    }

    // Scheduler
    scheduleDailyUpdate(ctx, updateTime)

    // HTTP handlers
    http.HandleFunc("/", indexHandler)
    http.HandleFunc("/favicon.png", faviconHandler)
    http.HandleFunc("/openapi.json", openapiHandler)
    http.HandleFunc("/api/v1/geoip", geoIPHandler())
    http.HandleFunc("/getdb", getDBHandler())

    srv := &http.Server{
        Addr:         listenAddr,
        ReadTimeout:  10 * time.Second,
        WriteTimeout: 10 * time.Second,
    }
    log.Printf("GeoIP server listening on %s", listenAddr)
    if err := srv.ListenAndServe(); err != nil {
        log.Fatalf("server stopped: %v", err)
    }
}
