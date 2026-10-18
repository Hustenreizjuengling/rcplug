package host

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Hustenreizjuengling/rcplug/sdk"
)

// Option is a setting of a plugin that users change in the dashboard, so no
// one needs a shell for it. The host turns a value into a command-line
// argument (Arg) or an environment variable (Env) when it starts the plugin;
// only values a user has set are passed, otherwise the plugin's own default
// applies (Default only tells the dashboard what that is). Values are kept in
// config/<plugin>.json under "options".
type Option struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"` // bool | choice | number | text
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Default     any      `json:"default,omitempty"`
	Choices     []Choice `json:"choices,omitempty"`   // choice
	Min         *float64 `json:"min,omitempty"`       // number
	Max         *float64 `json:"max,omitempty"`       // number
	MaxLength   int      `json:"maxLength,omitempty"` // text (default 256)
	Pattern     string   `json:"pattern,omitempty"`   // text: the whole value must match
	// Arg is the argument added to the command line: for bool the flag
	// itself (added when true, e.g. "--read-only"), otherwise a single
	// argument with {value} in it (e.g. "--meters={value}").
	Arg string `json:"arg,omitempty"`
	// Env is an environment variable set to the value ("1"/"0" for bool).
	Env string `json:"env,omitempty"`
}

// Choice is one value of a choice option.
type Choice struct {
	Value string `json:"value"`
	Title string `json:"title"`
}

// OptionTranslation is an option's title, description and choice titles in
// one language (Translation.Options, keyed by option id).
type OptionTranslation struct {
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Choices     map[string]string `json:"choices,omitempty"`
}

var (
	optionID = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)
	envName  = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)
	flagArg  = regexp.MustCompile(`^--?[A-Za-z0-9][A-Za-z0-9_-]*$`)
)

const maxText = 256

// validateOptions checks a manifest's option definitions.
func validateOptions(m *Manifest) error {
	seen := map[string]bool{}
	for i, o := range m.Options {
		if !optionID.MatchString(o.ID) || seen[o.ID] {
			return fmt.Errorf("option %d: id %q must be unique, start with a letter and use letters, digits, - and _", i, o.ID)
		}
		seen[o.ID] = true
		if strings.TrimSpace(o.Title) == "" {
			return fmt.Errorf("option %q: title is empty", o.ID)
		}
		if (o.Arg == "") == (o.Env == "") {
			return fmt.Errorf("option %q: set exactly one of arg and env", o.ID)
		}
		if o.Env != "" && (!envName.MatchString(o.Env) || strings.HasPrefix(o.Env, "RCPLUG_")) {
			return fmt.Errorf("option %q: env %q must be an upper-case name not starting with RCPLUG_", o.ID, o.Env)
		}
		switch o.Type {
		case "bool":
			if o.Arg != "" && !flagArg.MatchString(o.Arg) {
				return fmt.Errorf("option %q: a bool's arg is a flag such as --read-only", o.ID)
			}
		case "choice", "number", "text":
			if o.Arg != "" && (strings.Count(o.Arg, "{value}") != 1 || !strings.HasPrefix(o.Arg, "-")) {
				return fmt.Errorf("option %q: arg must be one flag argument with {value}, e.g. --level={value}", o.ID)
			}
		default:
			return fmt.Errorf("option %q: unknown type %q (bool, choice, number, text)", o.ID, o.Type)
		}
		switch o.Type {
		case "choice":
			if len(o.Choices) == 0 {
				return fmt.Errorf("option %q: a choice needs choices", o.ID)
			}
			values := map[string]bool{}
			for _, c := range o.Choices {
				if c.Value == "" || values[c.Value] || strings.TrimSpace(c.Title) == "" || !printable(c.Value) {
					return fmt.Errorf("option %q: choices need unique, non-empty values and titles", o.ID)
				}
				values[c.Value] = true
			}
		case "number":
			if o.Min != nil && o.Max != nil && *o.Min > *o.Max {
				return fmt.Errorf("option %q: min is above max", o.ID)
			}
		case "text":
			if o.MaxLength < 0 || o.MaxLength > 4096 {
				return fmt.Errorf("option %q: maxLength must be 0..4096", o.ID)
			}
			if o.Pattern != "" {
				if _, err := regexp.Compile(`^(?:` + o.Pattern + `)$`); err != nil {
					return fmt.Errorf("option %q: pattern: %v", o.ID, err)
				}
			}
		}
		if o.Default != nil {
			raw, _ := json.Marshal(o.Default)
			if _, err := o.value(raw); err != nil {
				return fmt.Errorf("option %q: default: %v", o.ID, err)
			}
		}
	}
	return nil
}

// value checks a JSON value against the option and returns it as text for
// the command line or the environment.
func (o Option) value(raw json.RawMessage) (string, error) {
	switch o.Type {
	case "bool":
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return "", fmt.Errorf("must be true or false")
		}
		return strconv.FormatBool(b), nil
	case "choice":
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", fmt.Errorf("must be one of the choices")
		}
		for _, c := range o.Choices {
			if c.Value == s {
				return s, nil
			}
		}
		return "", fmt.Errorf("must be one of the choices")
	case "number":
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return "", fmt.Errorf("must be a number")
		}
		if (o.Min != nil && f < *o.Min) || (o.Max != nil && f > *o.Max) {
			return "", fmt.Errorf("must be between %s and %s", bound(o.Min, "-∞"), bound(o.Max, "∞"))
		}
		return strconv.FormatFloat(f, 'f', -1, 64), nil
	case "text":
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", fmt.Errorf("must be text")
		}
		limit := o.MaxLength
		if limit == 0 {
			limit = maxText
		}
		if len(s) > limit || !printable(s) {
			return "", fmt.Errorf("must be at most %d printable characters", limit)
		}
		if o.Pattern != "" && !regexp.MustCompile(`^(?:`+o.Pattern+`)$`).MatchString(s) {
			return "", fmt.Errorf("has the wrong format")
		}
		return s, nil
	}
	return "", fmt.Errorf("unknown type")
}

func bound(v *float64, none string) string {
	if v == nil {
		return none
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

// printable: no control characters (also none of Unicode's).
func printable(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return false
		}
	}
	return true
}

// optionArgs turns the values a user set into arguments and environment
// variables. Values that no longer fit the manifest (a plugin update changed
// an option) are left out and reported.
func optionArgs(m *Manifest, cfg UserConfig) (args, env []string, problems []string) {
	for _, o := range m.Options {
		raw, ok := cfg.Options[o.ID]
		if !ok || string(raw) == "null" {
			continue
		}
		v, err := o.value(raw)
		if err != nil {
			problems = append(problems, fmt.Sprintf("option %s ignored: %v", o.ID, err))
			continue
		}
		switch {
		case o.Type == "bool" && o.Arg != "":
			if v == "true" {
				args = append(args, o.Arg)
			}
		case o.Arg != "":
			args = append(args, strings.Replace(o.Arg, "{value}", v, 1))
		case o.Type == "bool":
			env = append(env, o.Env+"="+map[bool]string{true: "1", false: "0"}[v == "true"])
		default:
			env = append(env, o.Env+"="+v)
		}
	}
	return args, env, problems
}

// SaveOptions validates values (id → value; null removes it, so the
// plugin's default applies again) against the plugin's options and stores
// them in config/<name>.json, keeping the file's args and env.
func SaveOptions(root string, m *Manifest, values map[string]json.RawMessage) error {
	byID := map[string]Option{}
	for _, o := range m.Options {
		byID[o.ID] = o
	}
	cfg, err := LoadUserConfig(root, m.Name)
	if err != nil {
		return err
	}
	if cfg.Options == nil {
		cfg.Options = map[string]json.RawMessage{}
	}
	for id, raw := range values {
		o, ok := byID[id]
		if !ok {
			return sdk.NewError(400, "unknown_option", fmt.Sprintf("%s has no option %q", m.Name, id))
		}
		if string(raw) == "null" || len(raw) == 0 {
			delete(cfg.Options, id)
			continue
		}
		if _, err := o.value(raw); err != nil {
			return sdk.NewError(400, "invalid_option", fmt.Sprintf("%s: %v", o.Title, err))
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			return sdk.NewError(400, "invalid_option", err.Error())
		}
		cfg.Options[id] = compact.Bytes()
	}
	if len(cfg.Options) == 0 {
		cfg.Options = nil
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		return err
	}
	return sdk.WriteFileAtomic(filepath.Join(root, "config", m.Name+".json"), append(out, '\n'), 0o644)
}
