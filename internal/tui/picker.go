package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// pickerItem is one row in a pickerScreen. Items are owned by the
// caller; the picker only stores them by reference.
type pickerItem struct {
	// Label is the primary text shown.
	Label string
	// Hint is an optional dim suffix (e.g. "(2 keys)").
	Hint string
	// Sentinel is true for synthetic rows like "+ Add new...". Sentinel
	// rows skip the delete handler.
	Sentinel bool
	// Data is an opaque payload returned to onSelect / onDelete so
	// callers don't need to track parallel slices.
	Data any
}

// pickerScreen is a reusable list-based screen. Drives the menu-driven
// UI: every place the user picks an existing object goes through this.
type pickerScreen struct {
	root      *rootModel
	title     string
	help      string
	items     []pickerItem
	cursor    int
	emptyHint string

	onSelect func(it pickerItem) tea.Cmd
	// onDelete is optional; if nil, 'd' is ignored.
	onDelete func(it pickerItem) tea.Cmd
	// onBack overrides the default popMsg behaviour for esc/q.
	onBack func() tea.Cmd
}

func newPickerScreen(r *rootModel, title string, items []pickerItem, onSelect func(pickerItem) tea.Cmd) *pickerScreen {
	return &pickerScreen{root: r, title: title, items: items, onSelect: onSelect}
}

func (p *pickerScreen) WithDelete(fn func(pickerItem) tea.Cmd) *pickerScreen {
	p.onDelete = fn
	p.help = "[a] add  [enter] select  [d] delete  [esc] back"
	return p
}

func (p *pickerScreen) WithEmptyHint(s string) *pickerScreen { p.emptyHint = s; return p }
func (p *pickerScreen) WithHelp(s string) *pickerScreen      { p.help = s; return p }
func (p *pickerScreen) WithBack(fn func() tea.Cmd) *pickerScreen {
	p.onBack = fn
	return p
}

func (p *pickerScreen) Title() string { return p.title }
func (p *pickerScreen) Init() tea.Cmd { return nil }

func (p *pickerScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case pickerRefreshMsg:
		// Caller can request a refresh via the message; the contents
		// are set via SetItems before the message arrives.
		return p, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if p.cursor > 0 {
				p.cursor--
			}
		case "down", "j":
			if p.cursor < len(p.items)-1 {
				p.cursor++
			}
		case "home", "g":
			p.cursor = 0
		case "end", "G":
			p.cursor = len(p.items) - 1
		case "enter", "right", "l":
			if len(p.items) == 0 || p.onSelect == nil {
				return p, nil
			}
			return p, p.onSelect(p.items[p.cursor])
		case "d":
			if len(p.items) == 0 || p.onDelete == nil {
				return p, nil
			}
			it := p.items[p.cursor]
			if it.Sentinel {
				return p, nil
			}
			return p, p.onDelete(it)
		case "esc", "q", "left", "h":
			if p.onBack != nil {
				return p, p.onBack()
			}
			return p, emit(popMsg{})
		}
	}
	return p, nil
}

// SetItems lets the caller refresh the list while keeping the screen
// in place (e.g. after add/delete on the underlying config).
func (p *pickerScreen) SetItems(items []pickerItem) {
	p.items = items
	if p.cursor >= len(items) {
		p.cursor = len(items) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
}

func (p *pickerScreen) View() string {
	var b strings.Builder
	if len(p.items) == 0 {
		hint := p.emptyHint
		if hint == "" {
			hint = "(empty)"
		}
		b.WriteString(hintStyle.Render(hint) + "\n")
	}
	for i, it := range p.items {
		marker := "  "
		label := it.Label
		labelStyle := itemStyle
		if i == p.cursor {
			marker = cursorStyle.Render("➜ ")
			labelStyle = cursorStyle
		}
		row := marker + labelStyle.Render(label)
		if it.Hint != "" {
			row += " " + hintStyle.Render(it.Hint)
		}
		b.WriteString(row + "\n")
	}
	if p.help != "" {
		b.WriteString("\n" + helpStyle.Render(p.help))
	} else if p.onDelete != nil {
		b.WriteString("\n" + helpStyle.Render("[enter] select  [d] delete  [esc] back"))
	} else {
		b.WriteString("\n" + helpStyle.Render("[enter] select  [esc] back"))
	}
	return b.String()
}

// pickerRefreshMsg is reserved for future use (re-pulling items).
type pickerRefreshMsg struct{}
