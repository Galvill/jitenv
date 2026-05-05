package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/gv/jitenv/pkg/source"
)

// formField is one row in a form.
type formField struct {
	source.ParamField
	input    textinput.Model
	revealed bool
}

// form is a vertical stack of textinput rows, used by both source and
// mapping/secret edit screens. It does not own a frame or footer — the
// containing screen renders both.
type form struct {
	fields []*formField
	cursor int
}

func newForm(schema []source.ParamField, initial map[string]string) *form {
	f := &form{}
	for _, sf := range schema {
		ti := textinput.New()
		ti.Placeholder = sf.Help
		ti.Prompt = ""
		ti.CharLimit = 4096
		if sf.Sensitive {
			ti.EchoMode = textinput.EchoPassword
			ti.EchoCharacter = '•'
		}
		if v, ok := initial[sf.Key]; ok {
			ti.SetValue(v)
		}
		f.fields = append(f.fields, &formField{ParamField: sf, input: ti})
	}
	if len(f.fields) > 0 {
		f.fields[0].input.Focus()
	}
	return f
}

// Values returns the current values keyed by ParamField.Key.
func (f *form) Values() map[string]string {
	out := make(map[string]string, len(f.fields))
	for _, fld := range f.fields {
		out[fld.Key] = fld.input.Value()
	}
	return out
}

// MissingRequired returns the labels of any required fields that are empty.
func (f *form) MissingRequired() []string {
	var out []string
	for _, fld := range f.fields {
		if fld.Required && strings.TrimSpace(fld.input.Value()) == "" {
			label := fld.Label
			if label == "" {
				label = fld.Key
			}
			out = append(out, label)
		}
	}
	return out
}

// Update handles navigation + per-field input. Caller invokes this
// from its own Update method; it does not consume submit/cancel keys.
func (f *form) Update(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "tab", "down":
			f.focus(f.cursor + 1)
			return nil
		case "shift+tab", "up":
			f.focus(f.cursor - 1)
			return nil
		case "ctrl+r":
			if len(f.fields) > 0 {
				cur := f.fields[f.cursor]
				if cur.Sensitive {
					cur.revealed = !cur.revealed
					if cur.revealed {
						cur.input.EchoMode = textinput.EchoNormal
					} else {
						cur.input.EchoMode = textinput.EchoPassword
					}
				}
			}
			return nil
		}
	}
	if len(f.fields) == 0 {
		return nil
	}
	var cmd tea.Cmd
	f.fields[f.cursor].input, cmd = f.fields[f.cursor].input.Update(msg)
	return cmd
}

func (f *form) focus(i int) {
	if len(f.fields) == 0 {
		return
	}
	if i < 0 {
		i = 0
	}
	if i >= len(f.fields) {
		i = len(f.fields) - 1
	}
	f.fields[f.cursor].input.Blur()
	f.cursor = i
	f.fields[i].input.Focus()
}

func (f *form) View() string {
	if len(f.fields) == 0 {
		return hintStyle.Render("(no fields)")
	}
	var b strings.Builder
	for i, fld := range f.fields {
		marker := "  "
		labelStyle := itemStyle
		if i == f.cursor {
			marker = cursorStyle.Render("➜ ")
			labelStyle = cursorStyle
		}
		label := fld.Label
		if label == "" {
			label = fld.Key
		}
		if fld.Required {
			label += " *"
		}
		if fld.Sensitive {
			label += " " + maskedStyle.Render("(sensitive)")
		}
		b.WriteString(marker + labelStyle.Render(label) + "\n")
		b.WriteString("    " + fld.input.View() + "\n")
		if fld.Help != "" {
			b.WriteString("    " + hintStyle.Render(fld.Help) + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}
