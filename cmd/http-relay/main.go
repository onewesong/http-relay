package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onewesong/http-relay/internal/relay"
	"github.com/onewesong/http-relay/internal/tui"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version)
		return
	}

	var dump bool
	var addHeaders repeatedStringFlag
	var modifyHeaders repeatedStringFlag

	showVersion := flag.Bool("version", false, "print version and exit")
	modeRaw := flag.String("mode", "regular", "target mode: regular or reverse:<url>")
	listen := flag.String("listen", "", "listen address, overrides --host and --port")
	host := flag.String("host", envOrDefault("HOST", "127.0.0.1"), "listen host")
	port := flag.String("port", envOrDefault("PORT", "8080"), "listen port")
	timeout := flag.Duration("timeout", 600*time.Second, "upstream request timeout; 0 means no timeout")
	dumpScopeRaw := flag.String("dump-scope", os.Getenv("WIRE_SCOPE"), "dump scope when dump is enabled: req, resp, req,resp")
	maskAuth := flag.Bool("mask-auth", false, "mask authentication headers in request dump")
	colorRaw := flag.String("color", "auto", "colorize output: auto, always, never")
	tuiFlag := flag.Bool("tui", false, "interactive collapsible TUI (implies dump of req+resp)")

	flag.BoolVar(&dump, "w", false, "dump inbound request headers and body")
	flag.BoolVar(&dump, "dump", false, "dump inbound request/response traffic")
	flag.Var(&addHeaders, "add-header", "add an upstream request header, repeatable: Name: value")
	flag.Var(&modifyHeaders, "modify-header", "set an upstream request header, repeatable: Name: value")

	flag.Usage = func() {
		name := filepath.Base(os.Args[0])
		out := flag.CommandLine.Output()

		fmt.Fprintf(out, "Usage:\n")
		fmt.Fprintf(out, "  %s [flags]\n", name)
		fmt.Fprintf(out, "  %s version\n\n", name)

		fmt.Fprintf(out, "Modes:\n")
		fmt.Fprintf(out, "  regular                       target URL comes from /{absolute-url} (default)\n")
		fmt.Fprintf(out, "  reverse:<url>                 reverse proxy to an upstream URL\n\n")

		fmt.Fprintf(out, "Examples:\n")
		fmt.Fprintf(out, "  %s --mode reverse:https://api.example.com --modify-header 'User-Agent: http-relay'\n", name)
		fmt.Fprintf(out, "  %s --add-header 'X-Debug: 1' -w\n\n", name)

		fmt.Fprintf(out, "Flags:\n")
		flag.PrintDefaults()

		fmt.Fprintf(out, "\nEnvironment Variables:\n")
		fmt.Fprintf(out, "  HOST                  listen host fallback (default: 127.0.0.1)\n")
		fmt.Fprintf(out, "  PORT                  listen port fallback (default: 8080)\n")
		fmt.Fprintf(out, "  WIRE_SCOPE            dump scope fallback when dump is enabled: req, resp, req,resp (default)\n")
		fmt.Fprintf(out, "  ALL_PROXY             proxy for both HTTP and HTTPS, highest priority\n")
		fmt.Fprintf(out, "  HTTP_PROXY            upstream proxy for HTTP targets\n")
		fmt.Fprintf(out, "  HTTPS_PROXY           upstream proxy for HTTPS targets\n")
		fmt.Fprintf(out, "  NO_PROXY              bypass proxy for matching hosts\n")
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	logger := log.Default()

	colorMode, colorOK := relay.ParseColorMode(*colorRaw)
	if !colorOK {
		logger.Printf("invalid color mode=%q, fallback to auto", *colorRaw)
	}
	palette := relay.NewPalette(colorMode, os.Stderr)
	if palette.Enabled() {
		// Colored output renders its own dim timestamps; drop the logger's prefix.
		logger.SetFlags(0)
	}

	if *timeout < 0 {
		logger.Fatalf("timeout must be >= 0 (0 means no timeout)")
	}

	mode, err := relay.ParseMode(*modeRaw)
	if err != nil {
		logger.Fatalf("invalid mode: %v", err)
	}

	headerRules, err := relay.ParseHeaderRules(addHeaders.values, modifyHeaders.values)
	if err != nil {
		logger.Fatalf("invalid header rule: %v", err)
	}

	addr := strings.TrimSpace(*listen)
	if addr == "" {
		addr = strings.TrimSpace(*host) + ":" + strings.TrimSpace(*port)
	}

	wireScope, scopeOK := relay.ParseDumpScope(*dumpScopeRaw)
	if !scopeOK {
		logger.Printf("invalid dump scope=%q, fallback to %q", *dumpScopeRaw, (relay.DumpScopeReq | relay.DumpScopeResp).String())
	}

	transport, proxySummary, err := relay.NewTransportFromEnv()
	if err != nil {
		logger.Fatalf("failed to build transport: %v", err)
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   *timeout,
	}

	if *tuiFlag {
		if !isTerminal(os.Stdout) {
			logger.Fatalf("--tui requires an interactive terminal on stdout")
		}
		// The TUI always captures full req+resp traffic and owns the screen.
		dump = true
		wireScope = relay.DumpScopeReq | relay.DumpScopeResp
		runTUI(client, addr, mode, proxySummary, *maskAuth, *timeout, wireScope, headerRules)
		return
	}

	handler := relay.NewHandlerWithOptions(client, logger, relay.HandlerOptions{
		TargetMode:  mode,
		HeaderRules: headerRules,
		DumpRequest: dump,
		DumpScope:   wireScope,
		MaskAuth:    *maskAuth,
		Palette:     palette,
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	logStartup(logger, palette, addr, mode, proxySummary, dump, wireScope, *maskAuth, *timeout, headerRules)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("server stopped: %v", err)
	}
}

// runTUI starts the relay server in the background and runs the interactive
// TUI on the main goroutine (it owns the terminal). It returns when the user
// quits the TUI.
func runTUI(client *http.Client, addr string, mode relay.TargetMode, proxySummary string, maskAuth bool, timeout time.Duration, wireScope relay.DumpScope, headerRules []relay.HeaderRule) {
	header := tuiHeader(addr, mode, proxySummary, timeout)
	prog, reporter := tui.New(header)

	handler := relay.NewHandlerWithOptions(client, log.Default(), relay.HandlerOptions{
		TargetMode:  mode,
		HeaderRules: headerRules,
		DumpRequest: true,
		DumpScope:   wireScope,
		MaskAuth:    maskAuth,
		Reporter:    reporter,
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Silence the standard logger so stray writes can't corrupt the screen;
	// surface a fatal listen error after the TUI exits instead.
	log.SetOutput(io.Discard)

	serveErr := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serveErr <- err
			prog.Quit() // a dead listener shouldn't leave the user staring at an empty UI
		}
	}()

	_, runErr := prog.Run()
	_ = server.Close()
	log.SetOutput(os.Stderr)

	select {
	case err := <-serveErr:
		log.Fatalf("server stopped: %v", err)
	default:
	}
	if runErr != nil {
		log.Fatalf("tui stopped: %v", runErr)
	}
}

func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// tuiHeader renders the relay configuration into a one-line status string.
func tuiHeader(addr string, mode relay.TargetMode, proxySummary string, timeout time.Duration) string {
	timeoutStr := timeout.String()
	if timeout == 0 {
		timeoutStr = "none"
	}
	return fmt.Sprintf("%s · %s · proxy=%s · timeout=%s",
		addr, mode.String(), proxySummary, timeoutStr)
}

type repeatedStringFlag struct {
	values []string
}

func (f *repeatedStringFlag) Set(value string) error {
	f.values = append(f.values, value)
	return nil
}

func (f *repeatedStringFlag) String() string {
	return strings.Join(f.values, ", ")
}

func logStartup(logger *log.Logger, palette relay.Palette, addr string, mode relay.TargetMode, proxySummary string, dump bool, dumpScope relay.DumpScope, maskAuth bool, timeout time.Duration, headerRules []relay.HeaderRule) {
	if !palette.Enabled() {
		logger.Printf("http-relay %s", version)
		logger.Printf("listen: %s", addr)
		logger.Printf("mode: %s", mode.String())
		logger.Printf("upstream proxy: %s", proxySummary)
		logger.Printf("timeout: %s", timeout)
		logger.Printf("dump: enabled=%t scope=%s mask_auth=%t", dump, dumpScope.String(), maskAuth)
		if len(headerRules) == 0 {
			logger.Printf("request header rules: none")
			return
		}
		logger.Printf("request header rules:")
		for _, rule := range headerRules {
			logger.Printf("  %s", rule.Summary())
		}
		return
	}

	timeoutStr := timeout.String()
	if timeout == 0 {
		timeoutStr = "none"
	}
	dumpStr := "disabled"
	if dump {
		dumpStr = fmt.Sprintf("enabled scope=%s mask_auth=%t", dumpScope.String(), maskAuth)
	}

	field := func(label, value string) string {
		return palette.Dim("│  ") + palette.Dim(fmt.Sprintf("%-8s", label)) + value
	}

	logger.Printf("%s %s", palette.Dim("┌─"), palette.Bold("http-relay "+version))
	logger.Print(field("listen", palette.URL(addr)))
	logger.Print(field("mode", mode.String()))
	logger.Print(field("proxy", proxySummary))
	logger.Print(field("timeout", timeoutStr))
	logger.Print(field("dump", dumpStr))
	if len(headerRules) == 0 {
		logger.Printf("%s %s", palette.Dim("└─"), palette.Dim("ready"))
		return
	}
	logger.Print(field("headers", ""))
	for _, rule := range headerRules {
		logger.Print(palette.Dim("│    ") + rule.Summary())
	}
	logger.Printf("%s %s", palette.Dim("└─"), palette.Dim("ready"))
}

func envOrDefault(key, defaultValue string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return defaultValue
}
