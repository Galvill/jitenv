package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// inputScreen asks the user to type a single value, with a commit
// button (default label "Apply") and a [ Back ] button row beneath.
// The buttons are first-class focusable widgets — Tab cycles between
// the input and the buttons, and Enter on a focused button activates
// it.
type inputScreen struct {
	root      *rootModel
	title     string
	prompt    string
	input     textinput.Model
	masked    bool
	allowBlnk bool
	onCommit  func(string) tea.Cmd
	err       string

	btnFocus int // -1 = input focused; else button idx
	buttons  []button
}

type inputOpts struct {
	Title       string
	Prompt      string
	Placeholder string
	Help        string
	Initial     string
	Masked      bool
	AllowBlank  bool // when false, empty values produce an error
	SaveLabel   string
	CancelLabel string
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

	saveLabel := opts.SaveLabel
	if saveLabel == "" {
		saveLabel = "Apply"
	}
	cancelLabel := opts.CancelLabel
	if cancelLabel == "" {
		cancelLabel = "Back"
	}
	btns := []button{newButton(saveLabel), newButton(cancelLabel)}
	if opts.Masked {
		btns = append([]button{newButton("Reveal")}, btns...)
	}

	return &inputScreen{
		root:      r,
		title:     opts.Title,
		prompt:    opts.Prompt,
		input:     ti,
		masked:    opts.Masked,
		allowBlnk: opts.AllowBlank,
		onCommit:  onCommit,
		btnFocus:  -1,
		buttons:   btns,
	}
}

func (s *inputScreen) Title() string  { return s.title }
func (s *inputScreen) Status() string { return defaultFormStatus }
func (s *inputScreen) Init() tea.Cmd  { return nil }

func (s *inputScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			return s, emit(popMsg{})
		case "tab":
			if s.btnFocus < 0 {
				s.btnFocus = 0
				s.input.Blur()
			} else if s.btnFocus < len(s.buttons)-1 {
				s.btnFocus++
			} else {
				s.btnFocus = -1
				s.input.Focus()
			}
			return s, nil
		case "shift+tab":
			if s.btnFocus < 0 {
				s.btnFocus = len(s.buttons) - 1
				s.input.Blur()
			} else if s.btnFocus > 0 {
				s.btnFocus--
			} else {
				s.btnFocus = -1
				s.input.Focus()
			}
			return s, nil
		case "left":
			if s.btnFocus > 0 {
				s.btnFocus--
			}
			return s, nil
		case "right":
			if s.btnFocus >= 0 && s.btnFocus < len(s.buttons)-1 {
				s.btnFocus++
			}
			return s, nil
		case "down":
			if s.btnFocus < 0 {
				s.btnFocus = 0
				s.input.Blur()
			}
			return s, nil
		case "up":
			if s.btnFocus >= 0 {
				s.btnFocus = -1
				s.input.Focus()
			}
			return s, nil
		case "enter":
			if s.btnFocus < 0 {
				return s, s.commit()
			}
			label := s.buttons[s.btnFocus].label
			switch label {
			case "Reveal":
				if s.input.EchoMode == textinput.EchoPassword {
					s.input.EchoMode = textinput.EchoNormal
					s.buttons[s.btnFocus] = newButton("Hide")
				} else {
					s.input.EchoMode = textinput.EchoPassword
					s.buttons[s.btnFocus] = newButton("Reveal")
				}
				return s, nil
			case "Hide":
				s.input.EchoMode = textinput.EchoPassword
				s.buttons[s.btnFocus] = newButton("Reveal")
				return s, nil
			case "Back", "Cancel":
				return s, emit(popMsg{})
			default:
				return s, s.commit()
			}
		}
	}
	if s.btnFocus < 0 {
		var cmd tea.Cmd
		s.input, cmd = s.input.Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *inputScreen) commit() tea.Cmd {
	val := s.input.Value()
	trimmed := strings.TrimSpace(val)
	if trimmed == "" && !s.allowBlnk {
		s.err = "value required"
		return emit(errorMsg(s.err))
	}
	if s.onCommit != nil {
		return s.onCommit(val)
	}
	return emit(popMsg{})
}

func (s *inputScreen) View() string {
	var b strings.Builder
	if s.prompt != "" {
		b.WriteString(s.prompt + "\n\n")
	}
	inputLabel := labelStyle.Render("value")
	if s.btnFocus >= 0 {
		inputLabel = mutedStyle.Render("value")
	}
	b.WriteString(inputLabel + "\n  " + s.input.View() + "\n\n")
	if s.err != "" {
		b.WriteString(errorStyle.Render(s.err) + "\n\n")
	}
	b.WriteString(renderButtonRow(s.buttons, s.btnFocus) + "\n")
	return b.String()
}
