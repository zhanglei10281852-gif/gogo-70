// Package cli implements the CableMend command line: nine subcommands over the
// same strictly decoded inputs, each able to emit either a JSON document or a
// deterministic text rendering.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"CableMend/internal/config"
	"CableMend/internal/jsonio"
	"CableMend/internal/model"
	"CableMend/internal/store"
	"CableMend/internal/validate"
)

// Version is the CLI version string.
const Version = "1.0.0"

// Format names.
const (
	FormatText = "text"
	FormatJSON = "json"
)

// env bundles the streams a command writes to.
type env struct {
	stdout io.Writer
	stderr io.Writer
}

// commandFunc is the signature of a subcommand.
type commandFunc func(env, []string) error

// command is one registered subcommand.
type command struct {
	name    string
	summary string
	run     commandFunc
}

// commands returns the registered subcommands in canonical order.
func commands() []command {
	return []command{
		{"validate", "strictly decode and cross-check every input document", cmdValidate},
		{"ingest", "append fault evidence to the local append-only store", cmdIngest},
		{"locate", "localize a fault and produce an uncertainty window", cmdLocate},
		{"assets", "list vessels and depots with transit and capability checks", cmdAssets},
		{"window", "compute workability windows from sea-state observations", cmdWindow},
		{"plan", "build a repair plan for one fault", cmdPlan},
		{"campaign", "plan repairs for several faults with vessel contention", cmdCampaign},
		{"verify", "check post-repair measurements against the loss budget", cmdVerify},
		{"report", "summarize the local store and verify the audit chain", cmdReport},
	}
}

// Run dispatches a command line and returns the process exit code.
func Run(stdout, stderr io.Writer, args []string) int {
	e := env{stdout: stdout, stderr: stderr}
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	case "version", "--version":
		fmt.Fprintf(stdout, "cablemend %s\n", Version)
		return 0
	}
	for _, cmd := range commands() {
		if cmd.name != args[0] {
			continue
		}
		if err := cmd.run(e, args[1:]); err != nil {
			if err == flag.ErrHelp {
				return 2
			}
			fmt.Fprintf(stderr, "cablemend %s: %v\n", cmd.name, err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stderr, "cablemend: unknown command %q\n", args[0])
	usage(stderr)
	return 2
}

// usage prints the command overview.
func usage(w io.Writer) {
	fmt.Fprintf(w, "cablemend %s - offline submarine cable fault localization and repair planning\n\n", Version)
	fmt.Fprintln(w, "usage: cablemend <command> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "commands:")
	for _, cmd := range commands() {
		fmt.Fprintf(w, "  %-9s %s\n", cmd.name, cmd.summary)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "common flags:")
	fmt.Fprintln(w, "  --config PATH   strict JSON configuration document")
	fmt.Fprintln(w, "  --store PATH    local store directory (default: config store_dir)")
	fmt.Fprintln(w, "  --format FORMAT text or json (default: text)")
	fmt.Fprintln(w, "  --out PATH      write the rendered output to a file instead of stdout")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "every timestamp in stored output derives from the input data; no wall clock is read")
}

// common holds the flags shared by every subcommand.
type common struct {
	configPath string
	storeDir   string
	format     string
	outPath    string
}

// register adds the shared flags to a flag set.
func (c *common) register(fs *flag.FlagSet) {
	fs.StringVar(&c.configPath, "config", "", "path to a strict JSON configuration document")
	fs.StringVar(&c.storeDir, "store", "", "local store directory")
	fs.StringVar(&c.format, "format", FormatText, "output format: text or json")
	fs.StringVar(&c.outPath, "out", "", "write output to this file instead of stdout")
}

// validateFormat normalizes the format flag.
func (c *common) validateFormat() error {
	switch c.format {
	case FormatText, FormatJSON:
		return nil
	default:
		return fmt.Errorf("unknown format %q, expected text or json", c.format)
	}
}

// isJSON reports whether JSON output was requested.
func (c *common) isJSON() bool { return c.format == FormatJSON }

// loadConfig reads the configuration document.
func (c *common) loadConfig() (config.Config, error) {
	return config.Load(c.configPath)
}

// resolveStore returns the store directory to use.
func (c *common) resolveStore(cfg config.Config) string {
	if strings.TrimSpace(c.storeDir) != "" {
		return c.storeDir
	}
	return cfg.StoreDir
}

// openStore prepares the store directory.
func (c *common) openStore(cfg config.Config) (*store.Store, error) {
	return store.Open(c.resolveStore(cfg))
}

// emit renders a document either as JSON or through the supplied text renderer.
func (c *common) emit(e env, doc any, text func(io.Writer) error) error {
	w, closer, err := c.writer(e.stdout)
	if err != nil {
		return err
	}
	if c.isJSON() {
		if err := jsonio.Encode(w, doc); err != nil {
			closer()
			return err
		}
		return closer()
	}
	if err := text(w); err != nil {
		closer()
		return err
	}
	return closer()
}

// writer resolves the output stream, creating parent directories for --out.
func (c *common) writer(fallback io.Writer) (io.Writer, func() error, error) {
	if strings.TrimSpace(c.outPath) == "" {
		return fallback, func() error { return nil }, nil
	}
	if dir := filepath.Dir(c.outPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, fmt.Errorf("create output directory: %w", err)
		}
	}
	f, err := os.Create(c.outPath)
	if err != nil {
		return nil, nil, fmt.Errorf("create output file: %w", err)
	}
	return f, f.Close, nil
}

// parse parses a flag set and rejects positional arguments.
func parse(fs *flag.FlagSet, e env, args []string) error {
	fs.SetOutput(e.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return nil
}

// requireFlag reports a missing mandatory flag.
func requireFlag(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("--%s is required", name)
	}
	return nil
}

// resolveAsOf picks the as-of instant: the explicit flag when given, otherwise
// the latest input timestamp so that runs stay reproducible.
func resolveAsOf(flagValue string, evidence []model.Evidence, faults []model.FaultRecord) (model.UTCTime, error) {
	if strings.TrimSpace(flagValue) != "" {
		return model.ParseUTC(flagValue)
	}
	var times []model.UTCTime
	for _, ev := range evidence {
		times = append(times, ev.ObservedAt)
	}
	for _, f := range faults {
		times = append(times, f.ReportedAt)
	}
	latest := model.LatestTime(times)
	if latest.IsZero() {
		return model.UTCTime{}, fmt.Errorf("cannot derive an as-of instant from the inputs; pass --as-of")
	}
	return latest, nil
}

// loadInputs decodes the requested documents and fails on validation errors.
func loadInputs(cfg config.Config, in validate.Inputs) (validate.Loaded, error) {
	loaded, err := validate.Load(in)
	if err != nil {
		return validate.Loaded{}, err
	}
	loaded.Config = cfg
	if loaded.SystemSet != nil {
		loaded.SystemSet = model.NewSystemSet(loaded.Systems, cfg.Localization.DefaultSlackFactor)
	}
	report := validate.Check(in, loaded)
	if !report.OK {
		return validate.Loaded{}, fmt.Errorf("input validation failed:\n%s", formatProblems(report.Errors))
	}
	return loaded, nil
}

// formatProblems renders validation problems as an indented list.
func formatProblems(problems []validate.Problem) string {
	lines := make([]string, 0, len(problems))
	for _, p := range problems {
		lines = append(lines, fmt.Sprintf("  - [%s] %s", p.Document, p.Detail))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
