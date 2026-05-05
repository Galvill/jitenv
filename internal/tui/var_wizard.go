package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/pkg/source"
)

// startVarWizard launches a chain of small screens that lets the user
// specify exactly one config.VarRef without typing any identifier that
// already exists in the config (sources, bags, bag keys, github
// scopes are all picker-driven).
//
// onComplete is invoked with the finished ref. It is responsible for:
//   - mutating the parent mapping (append or replace the var)
//   - returning a tea.Sequence that includes popUntilMsg to unwind the
//     wizard back to the parent screen.
func startVarWizard(r *rootModel, initial config.VarRef, onComplete func(config.VarRef) tea.Cmd) tea.Cmd {
	return emit(pushMsg{s: newPickSourceStep(r, initial, onComplete)})
}

// ---------- step 1: pick source ----------

func newPickSourceStep(r *rootModel, ref config.VarRef, onComplete func(config.VarRef) tea.Cmd) screen {
	names := configuredSourceNames(r)
	if len(names) == 0 {
		return newStubScreen(r, "No sources",
			"You haven't configured any sources yet.\nGo to Sources first and add one.")
	}
	items := make([]pickerItem, 0, len(names))
	for _, n := range names {
		items = append(items, pickerItem{
			Label: n,
			Hint:  "(" + r.cfg.Sources[n].Type + ")",
			Data:  n,
		})
	}
	p := newPickerScreen(r, "Pick source", items, func(it pickerItem) tea.Cmd {
		ref.Source = it.Data.(string)
		return emit(pushMsg{s: dispatchByType(r, ref, onComplete)})
	})
	p.help = "[enter] choose  [esc] cancel"
	return p
}

func dispatchByType(r *rootModel, ref config.VarRef, onComplete func(config.VarRef) tea.Cmd) screen {
	sc, ok := r.cfg.Sources[ref.Source]
	if !ok {
		return newStubScreen(r, "(error)", "source vanished")
	}
	switch sc.Type {
	case "local":
		return newPickBagStep(r, ref, onComplete)
	case "github":
		return newPickGithubScopeStep(r, ref, onComplete)
	default:
		// AWS, noop, and any 3rd-party source: free-form ref input form.
		return newGenericRefStep(r, ref, sc.Type, onComplete)
	}
}

// ---------- local: pick bag → pick mode → (pick key) → name ----------

func newPickBagStep(r *rootModel, ref config.VarRef, onComplete func(config.VarRef) tea.Cmd) screen {
	bags := bagNames(r)
	items := make([]pickerItem, 0, len(bags))
	for _, b := range bags {
		count := len(r.cfg.Secrets[b])
		items = append(items, pickerItem{
			Label: b,
			Hint:  fmt.Sprintf("(%d keys)", count),
			Data:  b,
		})
	}
	if len(items) == 0 {
		return newStubScreen(r, "No bags",
			"No local secret bags yet.\nGo to Local secrets first and add one.")
	}
	p := newPickerScreen(r, "Pick bag", items, func(it pickerItem) tea.Cmd {
		ref.Ref = it.Data.(string)
		return emit(pushMsg{s: newPickLocalModeStep(r, ref, onComplete)})
	})
	return p
}

func newPickLocalModeStep(r *rootModel, ref config.VarRef, onComplete func(config.VarRef) tea.Cmd) screen {
	bagKeys := sortedBagKeys(r, ref.Ref)
	items := []pickerItem{
		{Label: "Inject ALL keys from this bag",
			Hint: fmt.Sprintf("(%d env vars)", len(bagKeys)), Data: "all"},
		{Label: "Pick ONE key from this bag", Hint: "(one env var)", Data: "one"},
	}
	p := newPickerScreen(r, "Bag mode: "+ref.Ref, items, func(it pickerItem) tea.Cmd {
		mode := it.Data.(string)
		if mode == "all" {
			ref.Name = ""
			ref.Key = ""
			return finishVar(r, ref, onComplete)
		}
		return emit(pushMsg{s: newPickBagKeyStep(r, ref, onComplete)})
	})
	p.help = "[enter] choose  [esc] back"
	return p
}

func newPickBagKeyStep(r *rootModel, ref config.VarRef, onComplete func(config.VarRef) tea.Cmd) screen {
	keys := sortedBagKeys(r, ref.Ref)
	if len(keys) == 0 {
		return newStubScreen(r, "Empty bag", "This bag has no keys yet.")
	}
	items := make([]pickerItem, 0, len(keys))
	for _, k := range keys {
		items = append(items, pickerItem{Label: k, Data: k})
	}
	p := newPickerScreen(r, "Pick key in "+ref.Ref, items, func(it pickerItem) tea.Cmd {
		ref.Key = it.Data.(string)
		// Ask for env-var name; default to the key name.
		return emit(pushMsg{s: newEnvNameStep(r, ref, ref.Key, onComplete)})
	})
	return p
}

// ---------- github: pick scope → input identifiers → input variable name ----------

func newPickGithubScopeStep(r *rootModel, ref config.VarRef, onComplete func(config.VarRef) tea.Cmd) screen {
	items := []pickerItem{
		{Label: "Repo Variables", Hint: "ref = owner/repo", Data: "repo"},
		{Label: "Org Variables", Hint: "ref = org", Data: "org"},
		{Label: "Environment Variables", Hint: "ref = owner/repo + environment name", Data: "env"},
	}
	p := newPickerScreen(r, "Pick GitHub scope", items, func(it pickerItem) tea.Cmd {
		scope := it.Data.(string)
		if ref.Extra == nil {
			ref.Extra = map[string]string{}
		}
		ref.Extra["scope"] = scope
		return emit(pushMsg{s: newGithubIdentifierStep(r, ref, scope, onComplete)})
	})
	return p
}

func newGithubIdentifierStep(r *rootModel, ref config.VarRef, scope string, onComplete func(config.VarRef) tea.Cmd) screen {
	switch scope {
	case "org":
		return newInputScreen(r, inputOpts{
			Title: "Org name", Prompt: "GitHub org",
			Placeholder: "my-org", Initial: ref.Ref,
		}, func(val string) tea.Cmd {
			ref.Ref = strings.TrimSpace(val)
			return emit(pushMsg{s: newGithubVarNameStep(r, ref, onComplete)})
		})
	case "env":
		return newInputScreen(r, inputOpts{
			Title: "Owner/repo", Prompt: "GitHub owner/repo (e.g. acme/widgets)",
			Initial: ref.Ref,
		}, func(val string) tea.Cmd {
			ref.Ref = strings.TrimSpace(val)
			return emit(pushMsg{s: newInputScreen(r, inputOpts{
				Title: "Environment name", Prompt: "GitHub environment",
				Initial: ref.Extra["environment"],
			}, func(envVal string) tea.Cmd {
				ref.Extra["environment"] = strings.TrimSpace(envVal)
				return emit(pushMsg{s: newGithubVarNameStep(r, ref, onComplete)})
			})})
		})
	default: // "repo"
		return newInputScreen(r, inputOpts{
			Title: "Owner/repo", Prompt: "GitHub owner/repo (e.g. acme/widgets)",
			Initial: ref.Ref,
		}, func(val string) tea.Cmd {
			ref.Ref = strings.TrimSpace(val)
			return emit(pushMsg{s: newGithubVarNameStep(r, ref, onComplete)})
		})
	}
}

func newGithubVarNameStep(r *rootModel, ref config.VarRef, onComplete func(config.VarRef) tea.Cmd) screen {
	return newInputScreen(r, inputOpts{
		Title: "Variable name", Prompt: "Name of the GitHub Variable to read",
		Placeholder: "DEPLOY_FLAG", Initial: ref.Key,
	}, func(val string) tea.Cmd {
		ref.Key = strings.TrimSpace(val)
		return emit(pushMsg{s: newEnvNameStep(r, ref, ref.Key, onComplete)})
	})
}

// ---------- AWS / generic: input ref → optional JSON key → env name ----------

func newGenericRefStep(r *rootModel, ref config.VarRef, typeName string, onComplete func(config.VarRef) tea.Cmd) screen {
	prompt := fmt.Sprintf("Identifier for source %q (type=%s)", ref.Source, typeName)
	placeholder := ""
	switch typeName {
	case "aws":
		placeholder = "prod/myapp/db"
	case "noop":
		placeholder = "anything"
	}
	return newInputScreen(r, inputOpts{
		Title: "Reference", Prompt: prompt,
		Placeholder: placeholder, Initial: ref.Ref,
	}, func(val string) tea.Cmd {
		ref.Ref = strings.TrimSpace(val)
		return emit(pushMsg{s: newGenericKeyStep(r, ref, onComplete)})
	})
}

func newGenericKeyStep(r *rootModel, ref config.VarRef, onComplete func(config.VarRef) tea.Cmd) screen {
	return newInputScreen(r, inputOpts{
		Title:       "Sub-key (optional)",
		Prompt:      "JSON key inside the secret. Leave blank to take the whole value.",
		Placeholder: "url",
		Initial:     ref.Key,
		AllowBlank:  true,
	}, func(val string) tea.Cmd {
		ref.Key = strings.TrimSpace(val)
		defaultName := ref.Key
		if defaultName == "" {
			defaultName = ref.Name
		}
		return emit(pushMsg{s: newEnvNameStep(r, ref, defaultName, onComplete)})
	})
}

// ---------- env name (final input) ----------

func newEnvNameStep(r *rootModel, ref config.VarRef, defaultName string, onComplete func(config.VarRef) tea.Cmd) screen {
	return newInputScreen(r, inputOpts{
		Title:       "Env var name",
		Prompt:      "Name of the environment variable to inject.",
		Placeholder: "DATABASE_URL",
		Initial:     pickInitial(ref.Name, defaultName),
	}, func(val string) tea.Cmd {
		ref.Name = strings.TrimSpace(val)
		return finishVar(r, ref, onComplete)
	})
}

func pickInitial(existing, fallback string) string {
	if existing != "" {
		return existing
	}
	return fallback
}

// ---------- finish ----------

func finishVar(r *rootModel, ref config.VarRef, onComplete func(config.VarRef) tea.Cmd) tea.Cmd {
	return onComplete(ref)
}

// ---------- helpers ----------

func configuredSourceNames(r *rootModel) []string {
	out := make([]string, 0, len(r.cfg.Sources))
	for n := range r.cfg.Sources {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func bagNames(r *rootModel) []string {
	out := make([]string, 0, len(r.cfg.Secrets))
	for n := range r.cfg.Secrets {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func sortedBagKeys(r *rootModel, bag string) []string {
	kv := r.cfg.Secrets[bag]
	out := make([]string, 0, len(kv))
	for k := range kv {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// summariseVar returns a one-line description of a var for use in the
// mapping form's var list.
func summariseVar(r *rootModel, v config.VarRef) string {
	src, ok := r.cfg.Sources[v.Source]
	srcDesc := v.Source
	if !ok {
		srcDesc += "?"
	}
	switch {
	case ok && src.Type == "local" && v.Key == "" && v.Name == "":
		return fmt.Sprintf("ALL keys from local/%s", v.Ref)
	case ok && src.Type == "local":
		return fmt.Sprintf("%s ← local/%s.%s", v.Name, v.Ref, v.Key)
	case ok && src.Type == "github":
		scope := v.Extra["scope"]
		if scope == "" {
			scope = "repo"
		}
		return fmt.Sprintf("%s ← github(%s)/%s.%s", v.Name, scope, v.Ref, v.Key)
	default:
		extra := v.Ref
		if v.Key != "" {
			extra += "." + v.Key
		}
		if v.Name == "" {
			return fmt.Sprintf("ALL keys from %s/%s", srcDesc, extra)
		}
		return fmt.Sprintf("%s ← %s/%s", v.Name, srcDesc, extra)
	}
}

// silence the import.
var _ source.SecretRef
