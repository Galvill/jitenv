package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// inputScreen is a one-question screen that asks the user to type a
// single value, then invokes a callback. Used in the wizard flow for
// the few fields that genuinely have no enumerable choices (paths,
// AWS SecretIds, GitHub repo names, free-form names).
type inputScreen struct {
	root      *rootModel
	title     string
	prompt    string
	help      string
	input     textinput.Model
	masked    bool
	allowBlnk bool
	onCommit  func(string) tea.Cmd
	err       string
}

type inputOpts struct {
	Title       string
	Prompt      string
	Placeholder string
	Help        string
	Initial     string
	Masked      bool
	AllowBlank  bool // when false, empty values produce an error
}

func newInputScreen(r *rootModel, opts inputOpts, onCommit func(string) tea.Cmd) *inputScreen {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 8192
	ti.Placeholder = opts.Placeholder
	if opts.Initial != "" {
		ti.SetValue(opts.Initial)
	}
	if opts.Masked {
		ti.EchoMode = textinput.EchoPassword
		ti.EchoCharacter = '•'
	}
	ti.Focus()
	help := opts.Help
	if help == "" {
		help = "[enter] save  [esc] cancel"
		if opts.Masked {
			help = "[enter] save  [ctrl+r] reveal  [esc] cancel"
		}
	}
	return &inputScreen{
		root:      r,
		title:     opts.Title,
		prompt:    opts.Prompt,
		help:      help,
		input:     ti,
		masked:    opts.Masked,
		allowBlnk: opts.AllowBlank,
		onCommit:  onCommit,
	}
}

func (s *inputScreen) Title() string { return s.title }
func (s *inputScreen) Init() tea.Cmd { return nil }

func (s *inputScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			return s, emit(popMsg{})
		case "enter":
			val := s.input.Value()
			trimmed := strings.TrimSpace(val)
			if trimmed == "" && !s.allowBlnk {
				s.err = "value required"
				return s, nil
			}
			if s.onCommit != nil {
				return s, s.onCommit(val)
			}
			return s, emit(popMsg{})
		case "ctrl+r":
			if s.masked {
				if s.input.EchoMode == textinput.EchoPassword {
					s.input.EchoMode = textinput.EchoNormal
				} else {
					s.input.EchoMode = textinput.EchoPassword
				}
			}
			return s, nil
		}
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return s, cmd
}

func (s *inputScreen) View() string {
	var b strings.Builder
	if s.prompt != "" {
		b.WriteString(s.prompt + "\n\n")
	}
	b.WriteString("  " + s.input.View() + "\n")
	if s.err != "" {
		b.WriteString("\n" + errorStyle.Render(s.err) + "\n")
	}
	b.WriteString("\n" + helpStyle.Render(s.help))
	return b.String()
}
