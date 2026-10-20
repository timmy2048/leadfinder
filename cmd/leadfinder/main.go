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
	"strings"
	"time"

	"github.com/timmy2048/leadfinder/internal/directory"
	"github.com/timmy2048/leadfinder/internal/fetch"
	"github.com/timmy2048/leadfinder/internal/importer"

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

// netFlags: concurrency, proxies, rate limit, robots.
type netFlags struct {
	conc, perHostMS, retries *int
	proxies                  *string
	robots                   *bool
}

func addNetFlags(fs *flag.FlagSet) netFlags {
	return netFlags{
		conc:      fs.Int("c", 8, "concurrent website enrichments"),
		perHostMS: fs.Int("per-host-ms", 700, "min milliseconds between requests to the same host"),
		retries:   fs.Int("retries", 2, "HTTP retries with exponential backoff (429/5xx/network)"),
		proxies:   fs.String("proxies", env("LF_PROXIES", ""), "file with one proxy per line (http://, https://, socks5://user:pass@host:port), rotated round-robin"),
		robots:    fs.Bool("robots", true, "respect robots.txt when crawling"),
	}
}

func (n netFlags) client() *fetch.Client {
	o := fetch.DefaultOptions()
	o.PerHostDelay = time.Duration(*n.perHostMS) * time.Millisecond
	o.Retries = *n.retries
	o.RespectRobots = *n.robots
	if *n.proxies != "" {
		ps, err := fetch.LoadProxies(*n.proxies)
		if err != nil {
			log.Fatalf("proxies: %v", err)
		}
		o.Proxies = ps
		log.Printf("using %d proxies", len(ps))
	}
	return fetch.New(o)
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  leadfinder serve [-addr :8090] [-db leadfinder.db] [-gmaps URL | -gmaps-bin PATH | -gmaps-download]
  leadfinder run -category restaurant -location "Bath, UK" [-area "city centre"] [-country gb] [-lang en]
                 [-radius 5000]   # omit or 0 = whole city bbox
                 [-incremental=true] [-cell-size 2500] [-max-cells 40]
                 [-limit 20] [-max-results 5] [-only-reachable=true] [-out-unreachable u.csv] [-osm=true] [-gmaps-on] [-social] [-smtp]
                 [-dirs yelp,yellowpages,tripadvisor,booking,opentable,chamber] [-chamber-url URL]
                 [-max-pages 12] [-js=true] [-rdap=true] [-cc=true] [-guess]
                 [-c 8] [-per-host-ms 700] [-retries 2] [-proxies proxies.txt] [-robots=true]
                 [-out leads.csv] [-db leadfinder.db]
  leadfinder import -in outscraper.csv -category cafe [-location "Tampa, FL"] [-country us] [-max-results 5] [-gmaps-on] [-out leads.csv]
  leadfinder resume -id 12     # continue an interrupted job (skips scanned cells, reuses enriched leads)`)
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
		nf := addNetFlags(fs)
		resume := fs.Bool("resume", true, "re-queue jobs interrupted by a restart")
		_ = fs.Parse(os.Args[2:])
		st, err := store.Open(*db)
		if err != nil {
			log.Fatal(err)
		}
		gm := gf.resolve(context.Background())
		r := &pipeline.Runner{Store: st, OSM: osm.New(), GMapsURL: gm, Concurrency: *nf.conc, Fetch: nf.client()}
		srv := web.New(st, r)
		if *resume {
			jobs, _ := st.InterruptedJobs()
			for _, j := range jobs {
				if j.Params.ImportFile != "" {
					continue
				}
				j.Params.Incremental = true // skip cells already scanned, reuse checkpointed leads
				log.Printf("resuming job %d", j.ID)
				srv.Enqueue(j.ID, j.Params)
			}
		}
		st.MarkInterruptedImports()
		if !*resume {
			st.MarkInterrupted()
		}
		log.Printf("leadfinder UI on %s (gmaps service %s)", *addr, gm)
		log.Fatal(http.ListenAndServe(*addr, srv.Handler()))
	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		var p model.JobParams
		fs.StringVar(&p.Category, "category", "", "business category, e.g. restaurant, dentist or OSM tag amenity=cafe")
		fs.StringVar(&p.Location, "location", "", "city / region")
		fs.StringVar(&p.Area, "area", "", "optional district / street (finer place)")
		fs.StringVar(&p.Country, "country", "", "ISO country code (helps geocoding)")
		fs.StringVar(&p.Lang, "lang", "en", "language for Google Maps")
		fs.IntVar(&p.RadiusM, "radius", 0, "radius meters; 0 = whole place bounding box")
		fs.IntVar(&p.Limit, "limit", 20, "per-cell result hint (not a total)")
		fs.IntVar(&p.MaxResults, "max-results", 0, "pick best N reachable leads after scanning/enriching everything (tier, then GMB score); 0 = all")
		onlyReach := fs.Bool("only-reachable", true, "main CSV contains only leads with email; set false to include unreachable after them")
		outUnreach := fs.String("out-unreachable", "", "optional CSV path for the '暂不可触达' (no email) list")
		fs.BoolVar(&p.UseOSM, "osm", true, "use OpenStreetMap")
		fs.BoolVar(&p.UseGMaps, "gmaps-on", false, "use Google Maps (needs gosom service)")
		fs.IntVar(&p.GMapsMaxSec, "gmaps-max-sec", 600, "max seconds for each gmaps cell job")
		fs.BoolVar(&p.Social, "social", false, "enrich from public social profile pages (off by default)")
		fs.BoolVar(&p.SMTPCheck, "smtp", false, "SMTP mailbox check (needs outbound port 25)")
		inc := fs.Bool("incremental", true, "skip already-scanned coverage cells; expand radius as a ring")
		fs.IntVar(&p.CellSizeM, "cell-size", 2500, "coverage grid cell size in meters")
		fs.IntVar(&p.MaxCells, "max-cells", 40, "max cells to scan per source in one job")
		dirs := fs.String("dirs", "", "comma list of extra directory sources: "+strings.Join(directory.Names, ","))
		fs.StringVar(&p.ChamberURL, "chamber-url", "", "known Chamber of Commerce directory base URL")
		fs.IntVar(&p.MaxPages, "max-pages", 12, "max pages crawled per website")
		fs.BoolVar(&p.JSRender, "js", true, "headless Chrome fallback for JS-only sites")
		fs.BoolVar(&p.RDAP, "rdap", true, "RDAP/WHOIS registrant emails (non-redacted only)")
		fs.BoolVar(&p.CommonCrawl, "cc", true, "Common Crawl archived contact pages when site is down/empty")
		fs.BoolVar(&p.GuessEmails, "guess", false, "guess first@domain from owner names (marked guessed, never primary unless SMTP-verified)")
		nf := addNetFlags(fs)
		db := fs.String("db", env("LF_DB", "leadfinder.db"), "sqlite path")
		gf := addGmapsFlags(fs)
		out := fs.String("out", "", "CSV output path (default stdout)")
		_ = fs.Parse(os.Args[2:])
		if p.Category == "" || p.Location == "" {
			usage()
		}
		if *dirs != "" {
			p.Directories = strings.Split(*dirs, ",")
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
		r := &pipeline.Runner{Store: st, OSM: osm.New(), GMapsURL: gm, Concurrency: *nf.conc, Fetch: nf.client()}
		start := time.Now()
		bs, err := r.Run(ctx, id, p)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("elapsed %s", time.Since(start).Round(time.Second))
		writeOut(*out, *outUnreach, *onlyReach, bs, id)
	case "import":
		fs := flag.NewFlagSet("import", flag.ExitOnError)
		var p model.JobParams
		in := fs.String("in", "", "CSV exported from Outscraper/Apify/PhantomBuster")
		fs.StringVar(&p.Category, "category", "", "keyword (for scoring / Maps rank)")
		fs.StringVar(&p.Location, "location", "", "optional city for Maps rank")
		fs.StringVar(&p.Country, "country", "", "ISO country code")
		fs.IntVar(&p.MaxResults, "max-results", 0, "best N reachable")
		fs.BoolVar(&p.UseGMaps, "gmaps-on", false, "check Maps rank (needs gosom)")
		fs.BoolVar(&p.Social, "social", false, "public social pages")
		fs.IntVar(&p.MaxPages, "max-pages", 12, "max pages per website")
		fs.BoolVar(&p.JSRender, "js", true, "headless Chrome fallback")
		fs.BoolVar(&p.RDAP, "rdap", true, "RDAP emails")
		fs.BoolVar(&p.CommonCrawl, "cc", true, "Common Crawl fallback")
		fs.BoolVar(&p.GuessEmails, "guess", false, "guess emails from names")
		nf := addNetFlags(fs)
		db := fs.String("db", env("LF_DB", "leadfinder.db"), "sqlite path")
		gf := addGmapsFlags(fs)
		out := fs.String("out", "", "CSV output path")
		_ = fs.Parse(os.Args[2:])
		if *in == "" {
			usage()
		}
		f, err := os.Open(*in)
		if err != nil {
			log.Fatal(err)
		}
		bs, err := importer.Parse(f)
		f.Close()
		if err != nil {
			log.Fatal(err)
		}
		p.ImportFile = *in
		st, err := store.Open(*db)
		if err != nil {
			log.Fatal(err)
		}
		defer st.Close()
		id, _ := st.CreateJob(p)
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		gm := ""
		if p.UseGMaps {
			gm = gf.resolve(ctx)
		}
		r := &pipeline.Runner{Store: st, OSM: osm.New(), GMapsURL: gm, Concurrency: *nf.conc, Fetch: nf.client()}
		res, err := r.RunImport(ctx, id, p, bs)
		if err != nil {
			log.Fatal(err)
		}
		writeOut(*out, "", true, res, id)
	case "resume":
		fs := flag.NewFlagSet("resume", flag.ExitOnError)
		jid := fs.Int64("id", 0, "job id")
		nf := addNetFlags(fs)
		db := fs.String("db", env("LF_DB", "leadfinder.db"), "sqlite path")
		gf := addGmapsFlags(fs)
		out := fs.String("out", "", "CSV output path")
		_ = fs.Parse(os.Args[2:])
		st, err := store.Open(*db)
		if err != nil {
			log.Fatal(err)
		}
		defer st.Close()
		j, err := st.GetJob(*jid)
		if err != nil {
			log.Fatal(err)
		}
		p := j.Params
		p.Incremental = true
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		gm := ""
		if p.UseGMaps {
			gm = gf.resolve(ctx)
		}
		r := &pipeline.Runner{Store: st, OSM: osm.New(), GMapsURL: gm, Concurrency: *nf.conc, Fetch: nf.client()}
		bs, err := r.Run(ctx, j.ID, p)
		if err != nil {
			log.Fatal(err)
		}
		writeOut(*out, "", true, bs, j.ID)
	default:
		usage()
	}
}

func writeOut(out, outUnreach string, onlyReach bool, bs []model.Business, id int64) {
	w := os.Stdout
	if out != "" {
		f, err := os.Create(out)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		w = f
	}
	reach, unreach := pipeline.Partition(bs)
	main := reach
	if !onlyReach {
		main = bs
	}
	if err := store.WriteCSV(w, main); err != nil {
		log.Fatal(err)
	}
	if outUnreach != "" {
		f, err := os.Create(outUnreach)
		if err != nil {
			log.Fatal(err)
		}
		if err := store.WriteCSV(f, unreach); err != nil {
			log.Fatal(err)
		}
		f.Close()
	}
	log.Printf("job %s: %d reachable, %d unreachable (暂不可触达)", strconv.FormatInt(id, 10), len(reach), len(unreach))

}
