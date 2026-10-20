// Command leadfinder: personal B2B lead finder (no email sending).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"

	"github.com/timmy2048/leadfinder/internal/gmaps"
	"github.com/timmy2048/leadfinder/internal/model"
	"github.com/timmy2048/leadfinder/internal/osm"
	"github.com/timmy2048/leadfinder/internal/pipeline"
	"github.com/timmy2048/leadfinder/internal/store"
	"github.com/timmy2048/leadfinder/internal/web"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// gmapsFlags are shared native-mode options for the gosom scraper.
type gmapsFlags struct {
	url, bin, dir, addr *string
	auto                *bool
}

func addGmapsFlags(fs *flag.FlagSet) gmapsFlags {
	return gmapsFlags{
		url:  fs.String("gmaps", env("LF_GMAPS_URL", ""), "URL of an already running gosom web server (e.g. docker service)"),
		bin:  fs.String("gmaps-bin", env("LF_GMAPS_BIN", ""), "path to gosom google-maps-scraper binary to launch natively"),
		auto: fs.Bool("gmaps-download", env("LF_GMAPS_DOWNLOAD", "") == "1", "auto-download gosom release binary into ./bin and launch it"),
		dir:  fs.String("gmaps-data", env("LF_GMAPS_DATA", "gmapsdata"), "gosom data folder (native mode)"),
		addr: fs.String("gmaps-addr", env("LF_GMAPS_ADDR", "127.0.0.1:8081"), "listen address for launched gosom"),
	}
}

// resolve returns the gosom base URL, launching a native child if configured.
func (g gmapsFlags) resolve(ctx context.Context) string {
	if *g.url != "" {
		return *g.url
	}
	bin := *g.bin
	if bin == "" && *g.auto {
		var err error
		if bin, err = gmaps.Download(ctx, "bin"); err != nil {
			log.Fatalf("gosom download: %v", err)
		}
	}
	if bin == "" {
		return "http://localhost:8080"
	}
	log.Printf("launching gosom %s (first run installs Playwright browsers, may take minutes)", bin)
	_, base, err := gmaps.Launch(ctx, bin, *g.dir, *g.addr)
	if err != nil {
		log.Fatalf("gosom launch: %v", err)
	}
	return base
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  leadfinder serve [-addr :8090] [-db leadfinder.db] [-gmaps URL | -gmaps-bin PATH | -gmaps-download]
  leadfinder run -category restaurant -location "Bath, UK" [-area "city centre"] [-country gb] [-lang en]
                 [-radius 5000]   # omit or 0 = whole city bbox
                 [-incremental=true] [-cell-size 2500] [-max-cells 40]
                 [-limit 20] [-osm=true] [-gmaps-on] [-social] [-smtp]
                 [-out leads.csv] [-db leadfinder.db]`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		addr := fs.String("addr", env("LF_ADDR", ":8090"), "listen address")
		db := fs.String("db", env("LF_DB", "leadfinder.db"), "sqlite path")
		gf := addGmapsFlags(fs)
		conc := fs.Int("c", 4, "website crawl concurrency")
		_ = fs.Parse(os.Args[2:])
		st, err := store.Open(*db)
		if err != nil {
			log.Fatal(err)
		}
		st.MarkInterrupted()
		gm := gf.resolve(context.Background())
		r := &pipeline.Runner{Store: st, OSM: osm.New(), GMapsURL: gm, Concurrency: *conc}
		log.Printf("leadfinder UI on %s (gmaps service %s)", *addr, gm)
		log.Fatal(http.ListenAndServe(*addr, web.New(st, r).Handler()))
	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		var p model.JobParams
		fs.StringVar(&p.Category, "category", "", "business category, e.g. restaurant, dentist or OSM tag amenity=cafe")
		fs.StringVar(&p.Location, "location", "", "city / region")
		fs.StringVar(&p.Area, "area", "", "optional district / street (finer place)")
		fs.StringVar(&p.Country, "country", "", "ISO country code (helps geocoding)")
		fs.StringVar(&p.Lang, "lang", "en", "language for Google Maps")
		fs.IntVar(&p.RadiusM, "radius", 0, "radius meters; 0 = whole place bounding box")
		fs.IntVar(&p.Limit, "limit", 20, "max results hint per cell/source")
		fs.BoolVar(&p.UseOSM, "osm", true, "use OpenStreetMap")
		fs.BoolVar(&p.UseGMaps, "gmaps-on", false, "use Google Maps (needs gosom service)")
		fs.IntVar(&p.GMapsMaxSec, "gmaps-max-sec", 600, "max seconds for each gmaps cell job")
		fs.BoolVar(&p.Social, "social", false, "enrich from public social profile pages (off by default)")
		fs.BoolVar(&p.SMTPCheck, "smtp", false, "SMTP mailbox check (needs outbound port 25)")
		inc := fs.Bool("incremental", true, "skip already-scanned coverage cells; expand radius as a ring")
		fs.IntVar(&p.CellSizeM, "cell-size", 2500, "coverage grid cell size in meters")
		fs.IntVar(&p.MaxCells, "max-cells", 40, "max cells to scan per source in one job")
		db := fs.String("db", env("LF_DB", "leadfinder.db"), "sqlite path")
		gf := addGmapsFlags(fs)
		out := fs.String("out", "", "CSV output path (default stdout)")
		_ = fs.Parse(os.Args[2:])
		if p.Category == "" || p.Location == "" {
			usage()
		}
		pipeline.NormalizeWithIncrementalDefault(&p, *inc)
		st, err := store.Open(*db)
		if err != nil {
			log.Fatal(err)
		}
		defer st.Close()
		id, err := st.CreateJob(p)
		if err != nil {
			log.Fatal(err)
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		gm := ""
		if p.UseGMaps {
			gm = gf.resolve(ctx)
		}
		r := &pipeline.Runner{Store: st, OSM: osm.New(), GMapsURL: gm, Concurrency: 4}
		bs, err := r.Run(ctx, id, p)
		if err != nil {
			log.Fatal(err)
		}
		w := os.Stdout
		if *out != "" {
			f, err := os.Create(*out)
			if err != nil {
				log.Fatal(err)
			}
			defer f.Close()
			w = f
		}
		if err := store.WriteCSV(w, bs); err != nil {
			log.Fatal(err)
		}
		log.Printf("job %s: %d businesses", strconv.FormatInt(id, 10), len(bs))
	default:
		usage()
	}
}
