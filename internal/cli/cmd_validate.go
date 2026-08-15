package cli

import (
	"flag"
	"fmt"
	"io"

	"CableMend/internal/validate"
)

// cmdValidate strictly decodes every supplied document and reports problems.
func cmdValidate(e env, args []string) error {
	var (
		c   common
		in  validate.Inputs
		fs  = flag.NewFlagSet("validate", flag.ContinueOnError)
		all bool
	)
	c.register(fs)
	fs.StringVar(&in.SystemsPath, "systems", "", "cable systems JSON document")
	fs.StringVar(&in.EvidencePath, "evidence", "", "fault evidence JSONL file")
	fs.StringVar(&in.AssetsPath, "assets", "", "repair assets JSON document")
	fs.StringVar(&in.WeatherPath, "weather", "", "sea-state observations JSONL file")
	fs.StringVar(&in.PermitsPath, "permits", "", "permits JSON document")
	fs.StringVar(&in.FaultsPath, "faults", "", "faults JSON document")
	fs.StringVar(&in.VerificationPath, "verification", "", "post-repair verification JSONL file")
	fs.BoolVar(&all, "strict-warnings", false, "treat warnings as failures")
	if err := parse(fs, e, args); err != nil {
		return err
	}
	if err := c.validateFormat(); err != nil {
		return err
	}
	if err := requireFlag("systems", in.SystemsPath); err != nil {
		return err
	}
	in.ConfigPath = c.configPath

	loaded, err := validate.Load(in)
	if err != nil {
		return err
	}
	report := validate.Check(in, loaded)
	decimals := loaded.Config.Decimals()
	if err := c.emit(e, report, func(w io.Writer) error {
		return renderValidate(w, decimals, report)
	}); err != nil {
		return err
	}
	if !report.OK {
		return fmt.Errorf("%d validation errors", len(report.Errors))
	}
	if all && len(report.Warnings) > 0 {
		return fmt.Errorf("%d validation warnings with --strict-warnings", len(report.Warnings))
	}
	return nil
}

// renderValidate prints the validation report as text.
func renderValidate(w io.Writer, decimals int, report validate.Report) error {
	t := newText(w, decimals)
	t.heading("input validation")
	t.yesNo("ok", report.OK)
	t.count("errors", len(report.Errors))
	t.count("warnings", len(report.Warnings))
	t.blank()

	t.heading("documents")
	rows := make([][]string, 0, len(report.Documents))
	for _, doc := range report.Documents {
		rows = append(rows, []string{doc.Document, doc.Path, fmt.Sprintf("%d", doc.Records), doc.Note})
	}
	t.table([]string{"document", "path", "records", "note"}, rows)
	t.blank()

	t.heading("checks performed")
	for _, check := range report.Checks {
		t.line("  - %s", check)
	}
	t.blank()

	t.heading("errors")
	if len(report.Errors) == 0 {
		t.line("  (none)")
	}
	for _, p := range report.Errors {
		t.line("  [%s] %s", p.Document, p.Detail)
	}
	t.blank()

	t.heading("warnings")
	if len(report.Warnings) == 0 {
		t.line("  (none)")
	}
	for _, p := range report.Warnings {
		t.line("  [%s] %s", p.Document, p.Detail)
	}
	return t.done()
}
