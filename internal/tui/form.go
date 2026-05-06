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

// form is a vertical stack of textinput rows. The containing screen
// owns the surrounding frame and the button row; form only renders the
// labelled inputs and exposes focus-navigation helpers so the screen
// can decide when to leave the form and focus a button.
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

func (f *form) Values() map[string]string {
	out := make(map[string]string, len(f.fields))
	for _, fld := range f.fields {
		out[fld.Key] = fld.input.Value()
	}
	return out
}

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

// Update is called by the parent screen when focus is on the form.
// It DOES NOT consume tab/shift-tab — those are handled by the parent
// screen so it can move focus out of the form to the button row.
func (f *form) Update(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "down":
			f.focusNext()
			return nil
		case "up":
			f.focusPrev()
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

// Focus navigation helpers used by the parent screen so it can decide
// when tab moves out of the form.
func (f *form) atFirstField() bool { return len(f.fields) == 0 || f.cursor == 0 }              
func (f *form) atLastField() bool  { return len(f.fields) == 0 || f.cursor >= len(f.fields)-1 }
func (f *form) focusFirst()        { f.focus(0) }                                              
func (f *form) focusLast()         { f.focus(len(f.fields) - 1) }                              
func (f *form) focusNext()         { f.focus(f.cursor + 1) }                                   
func (f *form) focusPrev()         { f.focus(f.cursor - 1) }                                   

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
		return dimText("(no fields)")
	}
	var b strings.Builder
	for i, fld := range f.fields {
		focused := i == f.cursor
		label := fld.Label
		if label == "" {
			label = fld.Key
		}
		if fld.Required {
			label += " *"
		}
		ls := labelStyle
		if !focused {
			ls = mutedStyle
		}
		b.WriteString(ls.Render(label))
		if fld.Sensitive {
			b.WriteString("  " + maskedStyle.Render("(sensitive)"))
		}
		b.WriteString("\n  ")
		b.WriteString(fld.input.View())
		b.WriteString("\n")
		if fld.Help != "" && focused {
			b.WriteString("  " + hintStyle.Render(fld.Help) + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}
