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

	appconfig "github.com/onewesong/http-relay/internal/config"
	"github.com/onewesong/http-relay/internal/relay"
	relayscript "github.com/onewesong/http-relay/internal/script"
	"github.com/onewesong/http-relay/internal/tui"
	"github.com/onewesong/http-relay/internal/web"
	builtinplugins "github.com/onewesong/http-relay/plugins"
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
	configPathRaw := flag.String("config", "", "TOML configuration path (or HTTP_RELAY_CONFIG)")
	modeRaw := flag.String("mode", "regular", "target mode: regular or reverse:<url>")
	listen := flag.String("listen", "", "listen address, overrides --host and --port")
	host := flag.String("host", envOrDefault("HOST", "127.0.0.1"), "listen host")
	port := flag.String("port", envOrDefault("PORT", "7080"), "listen port")
	timeout := flag.Duration("timeout", 600*time.Second, "upstream request timeout; 0 means no timeout")
	dumpScopeRaw := flag.String("dump-scope", os.Getenv("WIRE_SCOPE"), "dump scope when dump is enabled: req, resp, req,resp")
	maskAuth := flag.Bool("mask-auth", false, "mask authentication headers in request dump")
	colorRaw := flag.String("color", "auto", "colorize output: auto, always, never")
	tuiFlag := flag.Bool("tui", false, "interactive collapsible TUI (implies dump of req+resp)")
	webFlag := flag.Bool("web", false, "serve a live web UI on --web-listen (implies dump of req+resp)")
	webListen := flag.String("web-listen", "127.0.0.1:7090", "listen address for the web UI")
	webTrustForwarded := flag.Bool("web-trust-forwarded-headers", false, "trust X-Forwarded-Proto/Host for Web origin and cookie handling")
	scriptPath := flag.String("script", "", "path to a JS file with onRequest/onResponse hooks that rewrite traffic")
	scriptTimeout := flag.Duration("script-timeout", relayscript.DefaultTimeout, "per-hook execution timeout")
	scriptReload := flag.String("script-reload", "watch", "hot-reload mode: watch, poll, or off")

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
		fmt.Fprintf(out, "  regular                       target URL comes from /{absolute-url}, optionally prefixed by namespace and/or @rewrite-profile (default)\n")
		fmt.Fprintf(out, "  reverse:<url>                 reverse proxy to an upstream URL\n\n")

		fmt.Fprintf(out, "Examples:\n")
		fmt.Fprintf(out, "  %s --mode reverse:https://api.example.com --modify-header 'User-Agent: http-relay'\n", name)
		fmt.Fprintf(out, "  %s --add-header 'X-Debug: 1' -w\n", name)
		fmt.Fprintf(out, "  %s --web --web-listen 127.0.0.1:7090\n\n", name)
		fmt.Fprintf(out, "  %s --config ./config.toml # then request /team-a/@openai/https://example.com\n\n", name)

		fmt.Fprintf(out, "Flags:\n")
		flag.PrintDefaults()

		fmt.Fprintf(out, "\nEnvironment Variables:\n")
		fmt.Fprintf(out, "  HOST                  listen host fallback (default: 127.0.0.1)\n")
		fmt.Fprintf(out, "  PORT                  listen port fallback (default: 7080)\n")
		fmt.Fprintf(out, "  WIRE_SCOPE            dump scope fallback when dump is enabled: req, resp, req,resp (default)\n")
		fmt.Fprintf(out, "  HTTP_RELAY_CONFIG     TOML configuration path fallback\n")
		fmt.Fprintf(out, "  WEB_AUTH_KEY          legacy global password for the Web UI\n")
		fmt.Fprintf(out, "  WEB_AUTH_JWT_SECRET   JWT HMAC secret override (requires TOML mode=jwt)\n")
		fmt.Fprintf(out, "  WEB_MAX_TRANSACTIONS_PER_NAMESPACE\n")
		fmt.Fprintf(out, "                        retained Web transactions per namespace (default: 100)\n")
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
	configPath := appconfig.ResolvePath(*configPathRaw)
	appCfg, configWarnings, err := appconfig.Load(configPath)
	if err != nil {
		logger.Fatalf("invalid config: %v", err)
	}
	for _, warning := range configWarnings {
		logger.Printf("config warning: %s", warning)
	}
	legacyWebAuthKey := os.Getenv("WEB_AUTH_KEY")
	webOptions, err := resolveWebOptions(appCfg, legacyWebAuthKey, *webTrustForwarded)
	if err != nil {
		logger.Fatal(err)
	}

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

	reloadMode, err := relayscript.ParseReloadMode(*scriptReload)
	if err != nil {
		logger.Fatalf("invalid --script-reload: %v", err)
	}

	// console.log output goes to stderr, except under the TUI which owns the
	// screen—there it is discarded to avoid corrupting the display.
	var scriptConsole io.Writer = os.Stderr
	if *tuiFlag {
		scriptConsole = io.Discard
	}
	scriptHTTP, err := relayscript.NewHTTPService(relayscript.HTTPOptions{
		Enabled:              appCfg.Rewrite.HTTP.Enabled,
		AllowedOrigins:       appCfg.Rewrite.HTTP.AllowedOrigins,
		DefaultTimeout:       appCfg.Rewrite.HTTP.Timeout.Duration,
		MaxTimeout:           appCfg.Rewrite.HTTP.MaxTimeout.Duration,
		MaxRequestBodyBytes:  appCfg.Rewrite.HTTP.MaxRequestBodyBytes,
		MaxResponseBodyBytes: appCfg.Rewrite.HTTP.MaxResponseBodyBytes,
		MaxCallsPerHook:      appCfg.Rewrite.HTTP.MaxCallsPerHook,
		FollowRedirects:      appCfg.Rewrite.HTTP.FollowRedirects,
		AllowPrivateNetworks: appCfg.Rewrite.HTTP.AllowPrivateNetworks,
	})
	if err != nil {
		logger.Fatalf("failed to configure script HTTP: %v", err)
	}
	httpInfo := scriptHTTP.Info()
	logger.Printf("script http: enabled=%t origins=%d timeout=%s max_timeout=%s request_body=%d response_body=%d calls_per_hook=%d redirects=%t private_networks=%t",
		httpInfo.Enabled, httpInfo.AllowedOrigins, httpInfo.DefaultTimeout, httpInfo.MaxTimeout,
		httpInfo.MaxRequestBodyBytes, httpInfo.MaxResponseBodyBytes, httpInfo.MaxCallsPerHook,
		httpInfo.FollowRedirects, httpInfo.AllowPrivateNetworks)

	// A script that fails to compile at startup is fatal—better to fail fast
	// than to silently relay without the rewrites the operator asked for.
	defaultScriptSource := ""
	defaultReloadMode := reloadMode
	if builtInName, ok := builtinplugins.IsBuiltIn(*scriptPath); ok {
		data, err := builtinplugins.ReadBuiltIn(builtInName)
		if err != nil {
			logger.Fatalf("failed to load built-in script %q: %v", *scriptPath, err)
		}
		defaultScriptSource = string(data)
		defaultReloadMode = relayscript.ReloadOff
	}
	engine, err := relayscript.New(relayscript.Options{Path: *scriptPath, Source: defaultScriptSource, Timeout: *scriptTimeout, Console: scriptConsole, HTTP: scriptHTTP})
	if err != nil {
		logger.Fatalf("failed to load script %q: %v", *scriptPath, err)
	}
	scriptRegistry, err := buildScriptRegistry(appCfg, engine, *scriptTimeout, defaultReloadMode, scriptConsole, scriptHTTP)
	if err != nil {
		logger.Fatalf("failed to load rewrite profiles: %v", err)
	}
	if engine != nil || len(scriptRegistry.Profiles()) > 0 {
		stop, werr := scriptRegistry.WatchAll(defaultReloadMode, func(profile string, rerr error) {
			label := "default script"
			if profile != "" {
				label = "rewrite profile " + profile
			}
			if rerr != nil {
				logger.Printf("%s reload failed (keeping previous version): %v", label, rerr)
				return
			}
			logger.Printf("%s reloaded", label)
		})
		if werr != nil {
			logger.Fatalf("failed to watch rewrite scripts: %v", werr)
		}
		defer stop()
	}

	scriptSummary := "disabled"
	if engine != nil {
		scriptSummary = fmt.Sprintf("%s (req=%t resp=%t reload=%s timeout=%s)",
			*scriptPath, engine.HasRequestHook(), engine.HasResponseHook(), defaultReloadMode, *scriptTimeout)
	}
	if profiles := scriptRegistry.Profiles(); len(profiles) > 0 {
		scriptSummary += fmt.Sprintf("; profiles=%d", len(profiles))
		for _, profile := range profiles {
			logger.Printf("rewrite profile: name=%s script=%s req=%t resp=%t reload=%s timeout=%s",
				profile.Name, profile.Path, profile.HasRequest, profile.HasResponse, profile.Reload, profile.Timeout)
		}
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

	if *tuiFlag && *webFlag {
		logger.Fatalf("--tui and --web are mutually exclusive")
	}

	if *tuiFlag {
		if !isTerminal(os.Stdout) {
			logger.Fatalf("--tui requires an interactive terminal on stdout")
		}
		// The TUI always captures full req+resp traffic and owns the screen.
		dump = true
		wireScope = relay.DumpScopeReq | relay.DumpScopeResp
		runTUI(client, addr, mode, proxySummary, *maskAuth, *timeout, wireScope, headerRules, scriptRegistry)
		return
	}

	if *webFlag {
		// The web viewer always captures full req+resp traffic, like the TUI.
		dump = true
		wireScope = relay.DumpScopeReq | relay.DumpScopeResp
		meta := web.Meta{
			Addr:    addr,
			Mode:    mode.String(),
			Proxy:   proxySummary,
			Timeout: timeoutLabel(*timeout),
			Version: version,
		}
		runWeb(client, addr, *webListen, mode, proxySummary, *maskAuth, *timeout, wireScope, headerRules, scriptRegistry, scriptSummary, meta, webOptions, logger, palette)
		return
	}

	handler := relay.NewHandlerWithOptions(client, logger, relay.HandlerOptions{
		TargetMode:     mode,
		HeaderRules:    headerRules,
		DumpRequest:    dump,
		DumpScope:      wireScope,
		MaskAuth:       *maskAuth,
		Palette:        palette,
		ScriptRegistry: scriptRegistry,
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	logStartup(logger, palette, addr, mode, proxySummary, dump, wireScope, *maskAuth, *timeout, headerRules, scriptSummary)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("server stopped: %v", err)
	}
}

func resolveWebOptions(cfg appconfig.Config, legacyKey string, trustForwarded bool) (web.Options, error) {
	opts := web.Options{
		AuthKey: legacyKey, MaxTransactionsPerNamespace: cfg.Web.MaxTransactionsPerNamespace,
		TrustForwardedHeaders: trustForwarded,
	}
	if cfg.Web.Auth.Mode != "jwt" {
		return opts, nil
	}
	if legacyKey != "" {
		return web.Options{}, fmt.Errorf("WEB_AUTH_KEY cannot be used together with web.auth JWT configuration")
	}
	opts.JWTAuth = &cfg.Web.Auth
	return opts, nil
}

func buildScriptRegistry(cfg appconfig.Config, defaultEngine *relayscript.Engine, defaultTimeout time.Duration, defaultReload relayscript.ReloadMode, console io.Writer, scriptHTTP *relayscript.HTTPService) (*relayscript.Registry, error) {
	profiles := make([]relayscript.ProfileOptions, 0, len(cfg.Rewrite.Profiles))
	for name, configured := range cfg.Rewrite.Profiles {
		source := ""
		timeout := defaultTimeout
		if configured.Timeout.Set {
			timeout = configured.Timeout.Duration
		}
		reload := defaultReload
		if configured.Reload != "" {
			parsed, err := relayscript.ParseReloadMode(configured.Reload)
			if err != nil {
				return nil, fmt.Errorf("rewrite profile %q: %w", name, err)
			}
			reload = parsed
		}
		if builtInName, ok := builtinplugins.IsBuiltIn(configured.Script); ok {
			data, err := builtinplugins.ReadBuiltIn(builtInName)
			if err != nil {
				return nil, fmt.Errorf("rewrite profile %q: %w", name, err)
			}
			source = string(data)
			reload = relayscript.ReloadOff
		}
		profiles = append(profiles, relayscript.ProfileOptions{
			Name: name, Path: configured.Script, Source: source, Timeout: timeout, Reload: reload, Console: console, HTTP: scriptHTTP,
		})
	}
	return relayscript.NewRegistry(defaultEngine, profiles)
}

// runTUI starts the relay server in the background and runs the interactive
// TUI on the main goroutine (it owns the terminal). It returns when the user
// quits the TUI.
func runTUI(client *http.Client, addr string, mode relay.TargetMode, proxySummary string, maskAuth bool, timeout time.Duration, wireScope relay.DumpScope, headerRules []relay.HeaderRule, scripts *relayscript.Registry) {
	header := tuiHeader(addr, mode, proxySummary, timeout)
	prog, reporter := tui.New(header)

	handler := relay.NewHandlerWithOptions(client, log.Default(), relay.HandlerOptions{
		TargetMode:     mode,
		HeaderRules:    headerRules,
		DumpRequest:    true,
		DumpScope:      wireScope,
		MaskAuth:       maskAuth,
		Reporter:       reporter,
		ScriptRegistry: scripts,
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

// runWeb starts the relay proxy server and the web-UI server side by side, each
// on its own listener (the proxy port treats any path as a target URL, so the
// UI cannot share it). It returns when either server stops.
func runWeb(client *http.Client, addr, webAddr string, mode relay.TargetMode, proxySummary string, maskAuth bool, timeout time.Duration, wireScope relay.DumpScope, headerRules []relay.HeaderRule, scripts *relayscript.Registry, scriptSummary string, meta web.Meta, webOptions web.Options, logger *log.Logger, palette relay.Palette) {
	webOptions.Logger = logger
	webHandler, reporter := web.New(meta, webOptions)

	proxyHandler := relay.NewHandlerWithOptions(client, logger, relay.HandlerOptions{
		TargetMode:     mode,
		HeaderRules:    headerRules,
		DumpRequest:    true,
		DumpScope:      wireScope,
		MaskAuth:       maskAuth,
		Reporter:       reporter,
		ScriptRegistry: scripts,
	})

	proxyServer := &http.Server{Addr: addr, Handler: proxyHandler, ReadHeaderTimeout: 10 * time.Second}
	webServer := &http.Server{Addr: webAddr, Handler: webHandler, ReadHeaderTimeout: 10 * time.Second}

	logStartup(logger, palette, addr, mode, proxySummary, true, wireScope, maskAuth, timeout, headerRules, scriptSummary)
	webURL := "http://" + webAddr
	if palette.Enabled() {
		logger.Printf("%s %s", palette.Dim("web UI:"), palette.URL(webURL))
	} else {
		logger.Printf("web UI: %s", webURL)
	}

	errc := make(chan error, 2)
	go func() { errc <- proxyServer.ListenAndServe() }()
	go func() { errc <- webServer.ListenAndServe() }()

	if err := <-errc; err != nil && err != http.ErrServerClosed {
		logger.Fatalf("server stopped: %v", err)
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
	return fmt.Sprintf("%s · %s · proxy=%s · timeout=%s",
		addr, mode.String(), proxySummary, timeoutLabel(timeout))
}

// timeoutLabel renders an upstream timeout for display, with 0 shown as "none".
func timeoutLabel(timeout time.Duration) string {
	if timeout == 0 {
		return "none"
	}
	return timeout.String()
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

func logStartup(logger *log.Logger, palette relay.Palette, addr string, mode relay.TargetMode, proxySummary string, dump bool, dumpScope relay.DumpScope, maskAuth bool, timeout time.Duration, headerRules []relay.HeaderRule, scriptSummary string) {
	if !palette.Enabled() {
		logger.Printf("http-relay %s", version)
		logger.Printf("listen: %s", addr)
		logger.Printf("mode: %s", mode.String())
		logger.Printf("upstream proxy: %s", proxySummary)
		logger.Printf("timeout: %s", timeout)
		logger.Printf("dump: enabled=%t scope=%s mask_auth=%t", dump, dumpScope.String(), maskAuth)
		logger.Printf("script: %s", scriptSummary)
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
	logger.Print(field("script", scriptSummary))
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
