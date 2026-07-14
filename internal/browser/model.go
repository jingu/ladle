package browser

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jingu/ladle/internal/apierror"
	"github.com/jingu/ladle/internal/editor"
	"github.com/jingu/ladle/internal/localpath"
	"github.com/jingu/ladle/internal/storage"
	"github.com/jingu/ladle/internal/uri"
)

const previewMaxBytes = 512 * 1024 // 512KB

// entry represents a single item in the tree.
type entry struct {
	name         string
	key          string
	isDir        bool
	isBucket     bool
	size         int64
	lastModified time.Time
}

// node represents a tree node.
type node struct {
	entry    entry
	depth    int
	expanded bool
	loaded   bool
	children []*node
}

// childrenLoadedMsg carries the loaded children back to Update.
type childrenLoadedMsg struct {
	parentKey string // identifies which node to populate
	children  []*node
	err       error
}

// editDoneMsg is sent after an edit operation completes.
type editDoneMsg struct {
	err     error
	message string
}

// newFileDoneMsg is sent after a new-file creation completes. On success the
// current directory is reloaded so the new object appears.
type newFileDoneMsg struct {
	err     error
	message string
}

// menuAction represents a context menu item.
type menuAction int

const (
	menuEdit menuAction = iota
	menuEditMeta
	menuDownload
	menuCopy
	menuMove
	menuDelete
	menuVersions
	menuNewFile // not shown in the context menu; triggered by the "n" key
)

var menuItems = []struct {
	action menuAction
	label  string
}{
	{menuEdit, "Edit"},
	{menuEditMeta, "Edit metadata"},
	{menuDownload, "Download to..."},
	{menuCopy, "Copy to..."},
	{menuMove, "Move to..."},
	{menuVersions, "Versions"},
	{menuDelete, "Delete"},
}

// tabCompleteMsg carries completion candidates back to the input handler.
type tabCompleteMsg struct {
	candidates []string
	prefix     string // the directory prefix used for listing
}

// localTabCompleteMsg carries local filesystem completion candidates.
type localTabCompleteMsg struct {
	candidates []string
}

// actionDoneMsg is sent after an async action (delete, copy, move) completes.
type actionDoneMsg struct {
	message string
	err     error
}

// versionsLoadedMsg carries the version list back to Update.
type versionsLoadedMsg struct {
	versions []storage.ObjectVersion
	err      error
}

// versionPreviewMsg carries the downloaded preview content back to Update.
type versionPreviewMsg struct {
	versionID string
	content   string
	err       error
}

// navigatedMsg is sent after navigation (goUp / bucket select) rebuilds the view.
type navigatedMsg struct {
	nodes   []*node
	header  string
	canGoUp bool
	bucket  string
	prefix  string
	err     error
}

// editCommand implements tea.ExecCommand to run the edit workflow
// while the TUI is suspended.
type editCommand struct {
	editFn  EditFunc
	uri     *uri.URI
	message string
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
}

func (c *editCommand) Run() error {
	msg, err := c.editFn(c.uri)
	c.message = msg
	return err
}
func (c *editCommand) SetStdin(r io.Reader)  { c.stdin = r }
func (c *editCommand) SetStdout(w io.Writer) { c.stdout = w }
func (c *editCommand) SetStderr(w io.Writer) { c.stderr = w }

// spinnerFrames defines the animation frames for the loading spinner.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// tickMsg advances the spinner animation.
type tickMsg struct{}

// model is the bubbletea Model for the browser.
type model struct {
	nodes   []*node
	cursor  int
	header  string
	version string

	canGoUp        bool      // whether backspace/.. should go up
	termHeight     int       // terminal height for scrolling
	termWidth      int       // terminal width for layout
	message        string    // status message (e.g. error from last action)
	messageIsError bool      // true if message is an error
	lastEscTime    time.Time // for double-Esc quit

	filtering  bool
	filterText string

	// Context menu state
	menuOpen   bool
	menuCursor int
	menuTarget *node // the file node the menu was opened for

	// Text input state (for download dir, copy/move destination)
	inputMode            bool
	inputPrompt          string
	inputText            string
	inputCursor          int // caret position within inputText, as a rune index
	inputAction          menuAction
	inputCandidates      []string // tab-completion candidates to list when ambiguous
	inputCandidateCursor int      // highlighted candidate; -1 = none selected

	// Confirm dialog state (for delete)
	confirmMode      bool
	confirmPrompt    string
	pendingDeleteKey string

	// Version mode state
	versionMode   bool
	versionCursor int
	versionList   []storage.ObjectVersion
	versionTarget *node

	// Version preview state
	previewContent string
	previewVersion string // versionID being displayed (avoid duplicate fetch)
	previewScroll  int
	previewLoading bool
	previewError   string

	initVersionKey string // set by --versions flag; triggers version loading on Init()

	quitting     bool
	loading      bool
	spinnerFrame int

	client           storage.Client
	ctx              context.Context // stored because bubbletea Cmd closures need it
	bucket           string
	prefix           string
	scheme           string
	browser          *Browser
	editFn           EditFunc
	editMetaFn       EditMetaFunc
	downloadFn       DownloadFunc
	restoreVersionFn RestoreVersionFunc
	newFileFn        NewFileFunc

	// New-file type/choice selection (optional popup shown after naming a new file)
	newFileChoices       []string
	newFileChoiceTitle   string
	newFileChoiceDefault int
	newFileChoosing      bool   // choice popup is open
	newFileName          string // name entered before the popup
	newFileChoiceCursor  int
}

func (m model) Init() tea.Cmd {
	if m.initVersionKey != "" {
		return tea.Batch(tea.ClearScreen, m.startLoading(), m.loadVersions(m.initVersionKey))
	}
	return tea.ClearScreen
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termHeight = msg.Height
		m.termWidth = msg.Width
		return m, nil
	case tea.KeyMsg:
		m.message = "" // clear message on any key
		m.messageIsError = false
		return m.handleKey(msg)
	case childrenLoadedMsg:
		return m.handleChildrenLoaded(msg)
	case editDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.message = apierror.Classify(msg.err).Error()
			m.messageIsError = true
		} else if msg.message != "" {
			m.message = msg.message
			m.messageIsError = false
		}
		return m, tea.ClearScreen
	case newFileDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.message = apierror.Classify(msg.err).Error()
			m.messageIsError = true
			return m, tea.ClearScreen
		}
		m.message = msg.message
		m.messageIsError = false
		// Reload so the created object shows up in the current listing.
		return m, tea.Batch(tea.ClearScreen, m.reloadView())
	case tickMsg:
		if m.loading {
			m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
			return m, tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
		}
		return m, nil
	case tabCompleteMsg:
		if m.inputMode {
			matches := matchingCandidates(m.inputText, msg.candidates)
			m.inputText = completeInput(m.inputText, msg.prefix, msg.candidates)
			m.inputCursor = len([]rune(m.inputText))
			m.inputCandidates = ambiguousCandidates(matches)
			m.inputCandidateCursor = -1
		}
		return m, nil
	case localTabCompleteMsg:
		if m.inputMode {
			matches := matchingCandidates(m.inputText, msg.candidates)
			m.inputText = completeLocalInput(m.inputText, msg.candidates)
			m.inputCursor = len([]rune(m.inputText))
			m.inputCandidates = ambiguousCandidates(matches)
			m.inputCandidateCursor = -1
		}
		return m, nil
	case actionDoneMsg:
		m.loading = false
		if msg.err != nil && msg.message != "" {
			m.message = msg.message + " (refresh failed: " + apierror.Classify(msg.err).Error() + ")"
			m.messageIsError = true
		} else if msg.err != nil {
			m.message = apierror.Classify(msg.err).Error()
			m.messageIsError = true
		} else {
			m.message = msg.message
			m.messageIsError = false
		}
		return m, nil
	case versionPreviewMsg:
		if !m.versionMode || msg.versionID != m.previewVersion {
			return m, nil
		}
		m.previewLoading = false
		if msg.err != nil {
			m.previewError = msg.err.Error()
			m.previewContent = ""
		} else {
			m.previewContent = msg.content
			m.previewError = ""
		}
		m.previewScroll = 0
		return m, nil
	case versionsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.message = apierror.Classify(msg.err).Error()
			m.messageIsError = true
			m.initVersionKey = ""
			return m, nil
		}
		if len(msg.versions) <= 1 {
			m.message = "No version history"
			m.messageIsError = true
			m.initVersionKey = ""
			return m, nil
		}
		// Auto-set versionTarget when launched via --versions
		if m.initVersionKey != "" && m.versionTarget == nil {
			name := filepath.Base(m.initVersionKey)
			m.versionTarget = &node{entry: entry{name: name, key: m.initVersionKey}}
			m.initVersionKey = ""
		}
		m.versionMode = true
		m.versionCursor = 0
		m.versionList = msg.versions
		m.previewContent = ""
		m.previewVersion = ""
		m.previewError = ""
		m.previewScroll = 0
		m, cmd := m.triggerPreview()
		return m, cmd
	case navigatedMsg:
		m.loading = false
		if msg.err != nil {
			m.message = apierror.Classify(msg.err).Error()
			m.messageIsError = true
			return m, nil
		}
		m.nodes = msg.nodes
		m.header = msg.header
		m.canGoUp = msg.canGoUp
		m.bucket = msg.bucket
		m.prefix = msg.prefix
		m.cursor = 0
		m.filtering = false
		m.filterText = ""
		return m, nil
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Version mode handling
	if m.versionMode {
		return m.handleVersionKey(msg)
	}

	// Confirm dialog handling (delete confirmation)
	if m.confirmMode {
		return m.handleConfirmKey(msg)
	}

	// Input mode handling (download dir, copy/move destination)
	if m.inputMode {
		return m.handleInputKey(msg)
	}

	// Context menu handling
	if m.menuOpen {
		return m.handleMenuKey(msg)
	}

	// New-file choice popup handling
	if m.newFileChoosing {
		return m.handleNewFileChoiceKey(msg)
	}

	// Filter mode handling
	if m.filtering {
		return m.handleFilterKey(msg)
	}

	// Esc handling: double-Esc to quit
	if msg.Type == tea.KeyEscape {
		now := time.Now()
		if !m.lastEscTime.IsZero() && now.Sub(m.lastEscTime) < 500*time.Millisecond {
			m.quitting = true
			return m, tea.Quit
		}
		m.lastEscTime = now
		return m, nil
	}
	m.lastEscTime = time.Time{} // reset on non-Esc key

	if m.loading {
		if msg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	}

	visible := m.visibleNodes()

	switch msg.String() {
	case "up", "k", "ctrl+p":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j", "ctrl+n":
		if m.cursor < len(visible)-1 {
			m.cursor++
		}
	case "enter":
		if len(visible) == 0 {
			break
		}
		n := visible[m.cursor]
		if n == nil {
			// ".." node — navigate up
			return m, m.navigateUp()
		}
		if n.entry.isBucket {
			// Select bucket — navigate into it
			return m, m.navigateToBucket(n.entry.name)
		}
		if n.entry.isDir {
			// Opening a directory while a confirmed filter is active should reveal
			// its full contents (consistent with how bucket/up navigation clears
			// the filter). Otherwise the expanded children stay filtered and the
			// directory looks empty or only shows matches.
			if m.filterText != "" {
				m, cmd := m.revealDir(n)
				return m, cmd
			}
			if !n.loaded {
				m.loading = true
				return m, tea.Batch(m.startLoading(), m.loadChildren(n))
			}
			n.expanded = !n.expanded
			if newVisible := m.visibleNodes(); m.cursor >= len(newVisible) {
				m.cursor = len(newVisible) - 1
			}
		} else {
			// File selected — exec edit with TUI suspended
			cmd, err := m.execEdit(n.entry.key)
			if err != nil {
				m.message = apierror.Classify(err).Error()
				m.messageIsError = true
				return m, nil
			}
			return m, cmd
		}
	case "right", "l", "ctrl+f":
		if len(visible) > 0 {
			n := visible[m.cursor]
			if n != nil && n.entry.isDir {
				if m.filterText != "" {
					m, cmd := m.revealDir(n)
					return m, cmd
				}
				if !n.loaded {
					m.loading = true
					return m, tea.Batch(m.startLoading(), m.loadChildren(n))
				}
				if !n.expanded {
					n.expanded = true
				}
			} else if n != nil && !n.entry.isDir && !n.entry.isBucket {
				// Open context menu for files
				m.menuOpen = true
				m.menuCursor = 0
				m.menuTarget = n
			}
		}
	case "left", "h", "ctrl+b":
		if len(visible) > 0 {
			n := visible[m.cursor]
			if n != nil && n.entry.isDir && n.expanded {
				n.expanded = false
				if newVisible := m.visibleNodes(); m.cursor >= len(newVisible) {
					m.cursor = len(newVisible) - 1
				}
			} else if n != nil && n.depth > 0 {
				if parent, idx := m.findParent(visible, m.cursor); parent != nil {
					parent.expanded = false
					m.cursor = idx
				}
			}
		}
	case "-":
		if m.canGoUp {
			return m, m.navigateUp()
		}
	case "/":
		m.filtering = true
		m.filterText = ""
	case "n":
		return m.startNewFile()
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

// startNewFile opens the text input for a new object name, prefilled with the
// current directory. It is a no-op in the S3 bucket-list root (no bucket to
// create in); SSM's bucketless root is a valid target.
func (m model) startNewFile() (tea.Model, tea.Cmd) {
	if m.newFileFn == nil {
		return m, nil
	}
	if m.bucket == "" && m.browser.bucketListEnabled {
		m.message = "Select a bucket first"
		m.messageIsError = true
		return m, nil
	}
	m.inputMode = true
	m.inputPrompt = "New file name"
	m.inputText = m.prefix
	m.inputCursor = len([]rune(m.inputText))
	m.inputAction = menuNewFile
	return m, nil
}

func (m model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.filtering = false
		m.filterText = ""
		m.cursor = 0
		return m, nil
	case tea.KeyEnter:
		m.filtering = false
		// keep filterText, clamp cursor
		if visible := m.visibleNodes(); m.cursor >= len(visible) {
			if len(visible) > 0 {
				m.cursor = len(visible) - 1
			} else {
				m.cursor = 0
			}
		}
		return m, nil
	case tea.KeyBackspace:
		if len(m.filterText) > 0 {
			m.filterText = m.filterText[:len(m.filterText)-1]
		}
		if m.filterText == "" {
			m.filtering = false
			m.cursor = 0
		} else {
			// clamp cursor after filter change
			if visible := m.visibleNodes(); m.cursor >= len(visible) && len(visible) > 0 {
				m.cursor = len(visible) - 1
			}
		}
		return m, nil
	case tea.KeyUp, tea.KeyDown:
		visible := m.visibleNodes()
		if msg.Type == tea.KeyUp && m.cursor > 0 {
			m.cursor--
		}
		if msg.Type == tea.KeyDown && m.cursor < len(visible)-1 {
			m.cursor++
		}
		return m, nil
	case tea.KeyCtrlP:
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case tea.KeyCtrlN:
		visible := m.visibleNodes()
		if m.cursor < len(visible)-1 {
			m.cursor++
		}
		return m, nil
	case tea.KeyCtrlC:
		m.quitting = true
		return m, tea.Quit
	case tea.KeyRunes:
		m.filterText += string(msg.Runes)
		m.cursor = 0 // reset cursor on new filter input
		return m, nil
	}
	return m, nil
}

func (m model) handleMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape, tea.KeyLeft:
		m.menuOpen = false
		m.menuTarget = nil
		return m, nil
	case tea.KeyUp:
		if m.menuCursor > 0 {
			m.menuCursor--
		}
		return m, nil
	case tea.KeyDown:
		if m.menuCursor < len(menuItems)-1 {
			m.menuCursor++
		}
		return m, nil
	case tea.KeyEnter:
		return m.executeMenuAction(menuItems[m.menuCursor].action)
	case tea.KeyCtrlC:
		m.quitting = true
		return m, tea.Quit
	}

	switch msg.String() {
	case "k", "ctrl+p":
		if m.menuCursor > 0 {
			m.menuCursor--
		}
	case "j", "ctrl+n":
		if m.menuCursor < len(menuItems)-1 {
			m.menuCursor++
		}
	}
	return m, nil
}

func (m model) handleNewFileChoiceKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.newFileChoosing = false
		m.newFileName = ""
		return m, nil
	case tea.KeyUp:
		if m.newFileChoiceCursor > 0 {
			m.newFileChoiceCursor--
		}
		return m, nil
	case tea.KeyDown:
		if m.newFileChoiceCursor < len(m.newFileChoices)-1 {
			m.newFileChoiceCursor++
		}
		return m, nil
	case tea.KeyEnter:
		choice := m.newFileChoices[m.newFileChoiceCursor]
		name := m.newFileName
		m.newFileChoosing = false
		m.newFileName = ""
		cmd, err := m.execNewFile(name, choice)
		if err != nil {
			m.message = apierror.Classify(err).Error()
			m.messageIsError = true
			return m, nil
		}
		return m, cmd
	case tea.KeyCtrlC:
		m.quitting = true
		return m, tea.Quit
	}

	switch msg.String() {
	case "k", "ctrl+p":
		if m.newFileChoiceCursor > 0 {
			m.newFileChoiceCursor--
		}
	case "j", "ctrl+n":
		if m.newFileChoiceCursor < len(m.newFileChoices)-1 {
			m.newFileChoiceCursor++
		}
	}
	return m, nil
}

func (m model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.confirmMode = false
		m.pendingDeleteKey = ""
		return m, nil
	case tea.KeyCtrlC:
		m.quitting = true
		return m, tea.Quit
	case tea.KeyRunes:
		ch := string(msg.Runes)
		if ch == "y" || ch == "Y" {
			key := m.pendingDeleteKey
			m.confirmMode = false
			m.pendingDeleteKey = ""
			m.loading = true
			return m, tea.Batch(m.startLoading(), m.deleteObject(key))
		}
		// Any other key (including "n", "N") cancels
		m.confirmMode = false
		m.pendingDeleteKey = ""
		return m, nil
	}
	// Enter without typing "y" = cancel (default No)
	if msg.Type == tea.KeyEnter {
		m.confirmMode = false
		m.pendingDeleteKey = ""
		return m, nil
	}
	return m, nil
}

func (m model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		// With a candidate list open, Esc just dismisses the list.
		if len(m.inputCandidates) > 0 {
			m.inputCandidates = nil
			m.inputCandidateCursor = -1
			return m, nil
		}
		m.inputMode = false
		m.inputText = ""
		m.inputCursor = 0
		return m, nil
	case tea.KeyUp, tea.KeyLeft:
		if len(m.inputCandidates) > 0 {
			m.inputCandidateCursor = prevCandidate(m.inputCandidateCursor, len(m.inputCandidates))
		}
		return m, nil
	case tea.KeyDown, tea.KeyRight:
		if len(m.inputCandidates) > 0 {
			m.inputCandidateCursor = nextCandidate(m.inputCandidateCursor, len(m.inputCandidates))
		}
		return m, nil
	case tea.KeyEnter:
		// A highlighted candidate is filled into the input first; a second
		// Enter (with no list) confirms the action.
		if len(m.inputCandidates) > 0 && m.inputCandidateCursor >= 0 {
			m.inputText = m.inputCandidates[m.inputCandidateCursor]
			m.inputCursor = len([]rune(m.inputText))
			m.inputCandidates = nil
			m.inputCandidateCursor = -1
			return m, nil
		}
		m.inputMode = false
		text := m.inputText
		m.inputText = ""
		m.inputCursor = 0
		m.inputCandidates = nil
		return m.executeInput(m.inputAction, text)
	case tea.KeyBackspace, tea.KeyCtrlH:
		m.inputText, m.inputCursor = deleteBefore(m.inputText, m.inputCursor)
		m.inputCandidates = nil
		m.inputCandidateCursor = -1
		return m, nil
	case tea.KeyCtrlA: // beginning of line
		m.inputCursor = 0
		return m, nil
	case tea.KeyCtrlE: // end of line
		m.inputCursor = len([]rune(m.inputText))
		return m, nil
	case tea.KeyCtrlB: // back one char
		if m.inputCursor > 0 {
			m.inputCursor--
		}
		return m, nil
	case tea.KeyCtrlF: // forward one char
		if m.inputCursor < len([]rune(m.inputText)) {
			m.inputCursor++
		}
		return m, nil
	case tea.KeyCtrlD: // delete char under cursor
		m.inputText, m.inputCursor = deleteAt(m.inputText, m.inputCursor)
		m.inputCandidates = nil
		m.inputCandidateCursor = -1
		return m, nil
	case tea.KeyCtrlK: // kill to end of line
		m.inputText, m.inputCursor = killToEnd(m.inputText, m.inputCursor)
		m.inputCandidates = nil
		m.inputCandidateCursor = -1
		return m, nil
	case tea.KeyCtrlU: // kill to beginning of line
		m.inputText, m.inputCursor = killToStart(m.inputText, m.inputCursor)
		m.inputCandidates = nil
		m.inputCandidateCursor = -1
		return m, nil
	case tea.KeyCtrlW: // kill previous word
		m.inputText, m.inputCursor = killWordBack(m.inputText, m.inputCursor)
		m.inputCandidates = nil
		m.inputCandidateCursor = -1
		return m, nil
	case tea.KeyTab:
		// With a candidate list already open, Tab cycles the highlight forward
		// (same as ↓/→) so repeated Tab presses step through the candidates.
		if len(m.inputCandidates) > 0 {
			m.inputCandidateCursor = nextCandidate(m.inputCandidateCursor, len(m.inputCandidates))
			return m, nil
		}
		if m.inputAction == menuCopy || m.inputAction == menuMove || m.inputAction == menuNewFile {
			return m, m.tabComplete(m.inputText)
		}
		if m.inputAction == menuDownload {
			return m, localTabComplete(m.inputText)
		}
		return m, nil
	case tea.KeyCtrlC:
		m.quitting = true
		return m, tea.Quit
	case tea.KeyRunes, tea.KeySpace:
		m.inputText, m.inputCursor = insertRunes(m.inputText, m.inputCursor, string(msg.Runes))
		m.inputCandidates = nil
		m.inputCandidateCursor = -1
		return m, nil
	}
	return m, nil
}

// Input line editing helpers. All operate on a rune index so multibyte object
// keys and file paths stay intact. Each returns the new text and caret
// position.

// insertRunes inserts ins at the caret and advances the caret past it.
func insertRunes(text string, cursor int, ins string) (string, int) {
	r := []rune(text)
	in := []rune(ins)
	out := make([]rune, 0, len(r)+len(in))
	out = append(out, r[:cursor]...)
	out = append(out, in...)
	out = append(out, r[cursor:]...)
	return string(out), cursor + len(in)
}

// deleteBefore removes the rune before the caret (Backspace / C-h).
func deleteBefore(text string, cursor int) (string, int) {
	if cursor <= 0 {
		return text, cursor
	}
	r := []rune(text)
	return string(append(r[:cursor-1], r[cursor:]...)), cursor - 1
}

// deleteAt removes the rune under the caret (C-d).
func deleteAt(text string, cursor int) (string, int) {
	r := []rune(text)
	if cursor >= len(r) {
		return text, cursor
	}
	return string(append(r[:cursor], r[cursor+1:]...)), cursor
}

// killToEnd drops everything from the caret onward (C-k).
func killToEnd(text string, cursor int) (string, int) {
	r := []rune(text)
	if cursor >= len(r) {
		return text, cursor
	}
	return string(r[:cursor]), cursor
}

// killToStart drops everything before the caret (C-u).
func killToStart(text string, cursor int) (string, int) {
	return string([]rune(text)[cursor:]), 0
}

// killWordBack drops the whitespace-delimited word before the caret (C-w).
func killWordBack(text string, cursor int) (string, int) {
	r := []rune(text)
	i := cursor
	for i > 0 && r[i-1] == ' ' {
		i--
	}
	for i > 0 && r[i-1] != ' ' {
		i--
	}
	return string(append(r[:i], r[cursor:]...)), i
}

// nextCandidate / prevCandidate move the candidate highlight with wrap-around.
// From the unselected state (-1) they land on the first / last entry.
func nextCandidate(cursor, n int) int {
	if cursor < 0 {
		return 0
	}
	return (cursor + 1) % n
}

func prevCandidate(cursor, n int) int {
	if cursor < 0 {
		return n - 1
	}
	return (cursor - 1 + n) % n
}

func (m model) executeMenuAction(action menuAction) (tea.Model, tea.Cmd) {
	m.menuOpen = false
	target := m.menuTarget

	if target == nil {
		return m, nil
	}

	switch action {
	case menuEdit:
		m.menuTarget = nil
		cmd, err := m.execEdit(target.entry.key)
		if err != nil {
			m.message = apierror.Classify(err).Error()
			m.messageIsError = true
			return m, nil
		}
		return m, cmd

	case menuEditMeta:
		m.menuTarget = nil
		cmd, err := m.execEditMeta(target.entry.key)
		if err != nil {
			m.message = apierror.Classify(err).Error()
			m.messageIsError = true
			return m, nil
		}
		return m, cmd

	case menuDownload:
		m.inputMode = true
		m.inputPrompt = "Download to directory"
		m.inputText = "./"
		m.inputCursor = len([]rune(m.inputText))
		m.inputAction = menuDownload
		return m, nil

	case menuDelete:
		m.menuTarget = nil
		key := target.entry.key
		m.confirmMode = true
		m.confirmPrompt = fmt.Sprintf("Delete %s? (y/N)", key)
		m.pendingDeleteKey = key
		return m, nil

	case menuCopy:
		m.inputMode = true
		m.inputPrompt = "Copy to path (same bucket)"
		m.inputText = target.entry.key
		m.inputCursor = len([]rune(m.inputText))
		m.inputAction = menuCopy
		return m, nil

	case menuMove:
		m.inputMode = true
		m.inputPrompt = "Move to path (same bucket)"
		m.inputText = target.entry.key
		m.inputCursor = len([]rune(m.inputText))
		m.inputAction = menuMove
		return m, nil

	case menuVersions:
		m.menuTarget = nil
		m.versionTarget = target
		m.loading = true
		return m, tea.Batch(m.startLoading(), m.loadVersions(target.entry.key))
	}
	return m, nil
}

func (m model) executeInput(action menuAction, text string) (tea.Model, tea.Cmd) {
	// New file has no menu target: it creates a fresh object at the typed key.
	if action == menuNewFile {
		if text == "" || strings.HasSuffix(text, "/") {
			m.message = "New file name must not be empty or end with /"
			m.messageIsError = true
			return m, nil
		}
		// With choices configured, pick one (arrow keys) before creating.
		if len(m.newFileChoices) > 0 {
			m.newFileChoosing = true
			m.newFileName = text
			m.newFileChoiceCursor = m.newFileChoiceDefault
			if m.newFileChoiceCursor < 0 || m.newFileChoiceCursor >= len(m.newFileChoices) {
				m.newFileChoiceCursor = 0
			}
			return m, nil
		}
		cmd, err := m.execNewFile(text, "")
		if err != nil {
			m.message = apierror.Classify(err).Error()
			m.messageIsError = true
			return m, nil
		}
		return m, cmd
	}

	target := m.menuTarget
	m.menuTarget = nil

	if target == nil || text == "" {
		return m, nil
	}

	switch action {
	case menuDownload:
		if m.downloadFn == nil {
			m.message = "Download not available"
			m.messageIsError = true
			return m, nil
		}
		cmd, err := m.execDownload(target.entry.key, text)
		if err != nil {
			m.message = apierror.Classify(err).Error()
			m.messageIsError = true
			return m, nil
		}
		return m, cmd

	case menuCopy:
		if text == target.entry.key {
			m.message = "Source and destination are the same"
			m.messageIsError = true
			return m, nil
		}
		m.loading = true
		return m, tea.Batch(m.startLoading(), m.copyObject(target.entry.key, text))

	case menuMove:
		if text == target.entry.key {
			m.message = "Source and destination are the same"
			m.messageIsError = true
			return m, nil
		}
		m.loading = true
		return m, tea.Batch(m.startLoading(), m.moveObject(target.entry.key, text))
	}
	return m, nil
}

func (m model) deleteObject(key string) tea.Cmd {
	client := m.client
	ctx := m.ctx
	bucket := m.bucket
	prefix := m.prefix
	b := m.browser
	return func() tea.Msg {
		if err := client.Delete(ctx, bucket, key); err != nil {
			return actionDoneMsg{err: err}
		}
		// Rebuild the view to reflect the deletion
		nodes, header, canGoUp, err := b.buildViewFor(ctx, bucket, prefix)
		if err != nil {
			return actionDoneMsg{message: "Deleted " + key, err: err}
		}
		return navigatedMsg{nodes: nodes, header: header, canGoUp: canGoUp, bucket: bucket, prefix: prefix}
	}
}

func (m model) copyObject(srcKey, dstKey string) tea.Cmd {
	client := m.client
	ctx := m.ctx
	bucket := m.bucket
	prefix := m.prefix
	b := m.browser
	return func() tea.Msg {
		if err := client.Copy(ctx, bucket, srcKey, dstKey); err != nil {
			return actionDoneMsg{err: err}
		}
		nodes, header, canGoUp, err := b.buildViewFor(ctx, bucket, prefix)
		if err != nil {
			return actionDoneMsg{message: "Copied to " + dstKey, err: err}
		}
		return navigatedMsg{nodes: nodes, header: header, canGoUp: canGoUp, bucket: bucket, prefix: prefix}
	}
}

func (m model) moveObject(srcKey, dstKey string) tea.Cmd {
	client := m.client
	ctx := m.ctx
	bucket := m.bucket
	prefix := m.prefix
	b := m.browser
	return func() tea.Msg {
		if err := client.Copy(ctx, bucket, srcKey, dstKey); err != nil {
			return actionDoneMsg{err: err}
		}
		if err := client.Delete(ctx, bucket, srcKey); err != nil {
			return actionDoneMsg{err: fmt.Errorf("copied but failed to delete source: %w", err)}
		}
		nodes, header, canGoUp, err := b.buildViewFor(ctx, bucket, prefix)
		if err != nil {
			return actionDoneMsg{message: "Moved to " + dstKey, err: err}
		}
		return navigatedMsg{nodes: nodes, header: header, canGoUp: canGoUp, bucket: bucket, prefix: prefix}
	}
}

// tabComplete fires an async command to list objects matching the input prefix.
func (m model) tabComplete(input string) tea.Cmd {
	client := m.client
	ctx := m.ctx
	bucket := m.bucket

	// Split input into directory prefix and partial name.
	// e.g. "path/to/fi" -> prefix="path/to/", partial="fi"
	dirPrefix := ""
	if idx := strings.LastIndex(input, "/"); idx >= 0 {
		dirPrefix = input[:idx+1]
	}

	return func() tea.Msg {
		entries, err := client.List(ctx, bucket, dirPrefix, "/")
		if err != nil {
			return tabCompleteMsg{candidates: nil, prefix: dirPrefix}
		}
		var candidates []string
		for _, e := range entries {
			candidates = append(candidates, e.Key)
		}
		return tabCompleteMsg{candidates: candidates, prefix: dirPrefix}
	}
}

// completeInput computes the completed input text from candidates.
func completeInput(input, dirPrefix string, candidates []string) string {
	return completeFromMatches(input, matchingCandidates(input, candidates))
}

// matchingCandidates returns the candidates that have input as a prefix.
func matchingCandidates(input string, candidates []string) []string {
	var matches []string
	for _, c := range candidates {
		if strings.HasPrefix(c, input) {
			matches = append(matches, c)
		}
	}
	return matches
}

// completeFromMatches extends input to the sole match, or to the longest common
// prefix when several match. Returns input unchanged when nothing extends it.
func completeFromMatches(input string, matches []string) string {
	if len(matches) == 0 {
		return input
	}
	if len(matches) == 1 {
		return matches[0]
	}
	lcp := matches[0]
	for _, m := range matches[1:] {
		lcp = longestCommonPrefix(lcp, m)
	}
	if len(lcp) > len(input) {
		return lcp
	}
	return input
}

// ambiguousCandidates returns matches only when more than one remains, so the
// UI lists them; a single or empty match needs no list.
func ambiguousCandidates(matches []string) []string {
	if len(matches) > 1 {
		return matches
	}
	return nil
}

// candidateLabel returns the trailing path element of a completion candidate
// (the part after the last "/"), keeping a trailing "/" for directories.
func candidateLabel(c string) string {
	trimmed := strings.TrimRight(c, "/")
	if i := strings.LastIndexByte(trimmed, '/'); i >= 0 {
		trimmed = trimmed[i+1:]
	}
	if strings.HasSuffix(c, "/") {
		return trimmed + "/"
	}
	return trimmed
}

// renderCandidates formats the tab-completion candidate list shown under the
// input line, highlighting the arrow-key selection and capping the count so a
// large directory does not flood the view.
func renderCandidates(candidates []string, cursor int) string {
	const max = 60
	// Slide the visible window so the highlighted candidate stays on screen even
	// when there are more candidates than the cap.
	start := 0
	if cursor >= max {
		start = cursor - max + 1
	}
	end := start + max
	if end > len(candidates) {
		end = len(candidates)
	}
	labels := make([]string, 0, max+2)
	if start > 0 {
		labels = append(labels, styleHelp.Render(fmt.Sprintf("(%d more) …", start)))
	}
	for i := start; i < end; i++ {
		label := candidateLabel(candidates[i])
		if i == cursor {
			labels = append(labels, styleMenuSelected.Render(label))
		} else {
			labels = append(labels, styleHelp.Render(label))
		}
	}
	if end < len(candidates) {
		labels = append(labels, styleHelp.Render(fmt.Sprintf("… (+%d more)", len(candidates)-end)))
	}
	return "  " + strings.Join(labels, "  ") + "\n"
}

// localTabComplete fires an async command to list local directory entries.
func localTabComplete(input string) tea.Cmd {
	return func() tea.Msg {
		dir := filepath.Dir(input)
		if dir == "" {
			dir = "."
		}
		// A bare "~" has no trailing slash, so filepath.Dir yields ".";
		// treat it as the home directory so its entries are listed.
		if input == "~" {
			dir = "~"
		}
		// Read the real directory (expanding a leading ~) but keep candidates in
		// the original form so they still prefix-match the user's typed input.
		entries, err := os.ReadDir(localpath.ExpandTilde(dir))
		if err != nil {
			return localTabCompleteMsg{candidates: nil}
		}
		var candidates []string
		for _, e := range entries {
			name := filepath.Join(dir, e.Name())
			if e.IsDir() {
				name += string(os.PathSeparator)
			}
			candidates = append(candidates, name)
		}
		return localTabCompleteMsg{candidates: candidates}
	}
}

// completeLocalInput computes completed local path from candidates.
func completeLocalInput(input string, candidates []string) string {
	return completeFromMatches(input, matchingCandidates(input, candidates))
}

// longestCommonPrefix returns the longest common prefix of two strings.
func longestCommonPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return a[:i]
		}
	}
	return a[:n]
}

// execEditMeta returns a tea.Exec command that suspends the TUI and runs the metadata edit.
func (m model) execEditMeta(key string) (tea.Cmd, error) {
	if m.editMetaFn == nil {
		return nil, fmt.Errorf("metadata editing not available")
	}
	raw := fmt.Sprintf("%s://%s/%s", m.scheme, m.bucket, key)
	u, err := uri.Parse(raw)
	if err != nil {
		return nil, err
	}
	c := &editMetaCommand{editMetaFn: m.editMetaFn, uri: u}
	return tea.Exec(c, func(err error) tea.Msg {
		return editDoneMsg{err: err, message: c.message}
	}), nil
}

// execDownload returns a tea.Exec command that suspends the TUI and downloads the file.
func (m model) execDownload(key, dir string) (tea.Cmd, error) {
	raw := fmt.Sprintf("%s://%s/%s", m.scheme, m.bucket, key)
	u, err := uri.Parse(raw)
	if err != nil {
		return nil, err
	}
	c := &downloadCommand{downloadFn: m.downloadFn, uri: u, dir: dir}
	return tea.Exec(c, func(err error) tea.Msg {
		return editDoneMsg{err: err, message: c.message}
	}), nil
}

// editMetaCommand implements tea.ExecCommand to run metadata editing
// while the TUI is suspended.
type editMetaCommand struct {
	editMetaFn EditMetaFunc
	uri        *uri.URI
	message    string
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
}

func (c *editMetaCommand) Run() error {
	msg, err := c.editMetaFn(c.uri)
	c.message = msg
	return err
}
func (c *editMetaCommand) SetStdin(r io.Reader)  { c.stdin = r }
func (c *editMetaCommand) SetStdout(w io.Writer) { c.stdout = w }
func (c *editMetaCommand) SetStderr(w io.Writer) { c.stderr = w }

// downloadCommand implements tea.ExecCommand to download a file
// while the TUI is suspended.
type downloadCommand struct {
	downloadFn DownloadFunc
	uri        *uri.URI
	dir        string
	message    string
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
}

func (c *downloadCommand) Run() error {
	msg, err := c.downloadFn(c.uri, c.dir)
	c.message = msg
	return err
}
func (c *downloadCommand) SetStdin(r io.Reader)  { c.stdin = r }
func (c *downloadCommand) SetStdout(w io.Writer) { c.stdout = w }
func (c *downloadCommand) SetStderr(w io.Writer) { c.stderr = w }

func (m model) handleVersionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.versionMode = false
		m.versionList = nil
		m.versionTarget = nil
		m.previewContent = ""
		m.previewVersion = ""
		m.previewError = ""
		m.previewLoading = false
		m.previewScroll = 0
		return m, nil
	case tea.KeyUp:
		if m.versionCursor > 0 {
			m.versionCursor--
		}
		m, cmd := m.triggerPreview()
		return m, cmd
	case tea.KeyDown:
		if m.versionCursor < len(m.versionList)-1 {
			m.versionCursor++
		}
		m, cmd := m.triggerPreview()
		return m, cmd
	case tea.KeyEnter:
		if len(m.versionList) == 0 {
			return m, nil
		}
		v := m.versionList[m.versionCursor]
		if v.IsLatest {
			m.message = "Already the current version"
			m.messageIsError = true
			m.versionMode = false
			m.versionList = nil
			m.versionTarget = nil
			return m, nil
		}
		if v.IsDeleteMarker {
			m.message = "Cannot restore a delete marker"
			m.messageIsError = true
			m.versionMode = false
			m.versionList = nil
			m.versionTarget = nil
			return m, nil
		}
		key := m.versionTarget.entry.key
		versionID := v.VersionID
		m.versionMode = false
		m.versionList = nil
		m.versionTarget = nil
		cmd, err := m.execRestoreVersion(key, versionID)
		if err != nil {
			m.message = apierror.Classify(err).Error()
			m.messageIsError = true
			return m, nil
		}
		return m, cmd
	case tea.KeyCtrlC:
		m.quitting = true
		return m, tea.Quit
	}

	switch msg.String() {
	case "k", "ctrl+p":
		if m.versionCursor > 0 {
			m.versionCursor--
		}
		m, cmd := m.triggerPreview()
		return m, cmd
	case "j", "ctrl+n":
		if m.versionCursor < len(m.versionList)-1 {
			m.versionCursor++
		}
		m, cmd := m.triggerPreview()
		return m, cmd
	case "ctrl+d":
		m.previewScroll += m.previewPageSize() / 2
		m.clampPreviewScroll()
		return m, nil
	case "ctrl+u":
		m.previewScroll -= m.previewPageSize() / 2
		m.clampPreviewScroll()
		return m, nil
	}
	return m, nil
}

// previewPageSize returns the number of visible content lines in the preview pane.
// Matches the right pane's content area (version list visible items).
func (m model) previewPageSize() int {
	h := m.versionPaneHeight()
	if h < 1 {
		return 1
	}
	return h
}

// clampPreviewScroll clamps previewScroll to valid range.
func (m *model) clampPreviewScroll() {
	if m.previewScroll < 0 {
		m.previewScroll = 0
	}
	lines := strings.Count(m.previewContent, "\n") + 1
	maxScroll := lines - m.previewPageSize()
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.previewScroll > maxScroll {
		m.previewScroll = maxScroll
	}
}

// triggerPreview checks the current version cursor and initiates preview fetch if needed.
// Returns the updated model and a command (or nil).
func (m model) triggerPreview() (model, tea.Cmd) {
	if len(m.versionList) == 0 || m.versionTarget == nil {
		return m, nil
	}
	v := m.versionList[m.versionCursor]
	if v.VersionID == m.previewVersion {
		return m, nil
	}
	m.previewVersion = v.VersionID
	m.previewScroll = 0
	if v.IsDeleteMarker {
		m.previewLoading = false
		m.previewContent = ""
		m.previewError = "Delete marker"
		return m, nil
	}
	if v.Size > previewMaxBytes {
		m.previewLoading = false
		m.previewContent = ""
		m.previewError = "File too large to preview (>512KB)"
		return m, nil
	}
	m.previewLoading = true
	m.previewContent = ""
	m.previewError = ""
	return m, m.loadVersionPreview(m.versionTarget.entry.key, v.VersionID)
}

func (m model) loadVersionPreview(key, versionID string) tea.Cmd {
	client := m.client
	ctx := m.ctx
	bucket := m.bucket
	return func() tea.Msg {
		var buf bytes.Buffer
		if err := client.DownloadVersion(ctx, bucket, key, versionID, &buf); err != nil {
			return versionPreviewMsg{versionID: versionID, err: err}
		}
		data := buf.Bytes()
		if editor.IsBinary(data) {
			return versionPreviewMsg{versionID: versionID, err: fmt.Errorf("binary file")}
		}
		return versionPreviewMsg{versionID: versionID, content: string(data)}
	}
}

func (m model) loadVersions(key string) tea.Cmd {
	client := m.client
	ctx := m.ctx
	bucket := m.bucket
	return func() tea.Msg {
		versions, err := client.ListVersions(ctx, bucket, key)
		return versionsLoadedMsg{versions: versions, err: err}
	}
}

// restoreVersionCommand implements tea.ExecCommand to run the restore workflow
// while the TUI is suspended.
type restoreVersionCommand struct {
	restoreVersionFn RestoreVersionFunc
	uri              *uri.URI
	versionID        string
	message          string
	stdin            io.Reader
	stdout           io.Writer
	stderr           io.Writer
}

func (c *restoreVersionCommand) Run() error {
	msg, err := c.restoreVersionFn(c.uri, c.versionID)
	c.message = msg
	return err
}
func (c *restoreVersionCommand) SetStdin(r io.Reader)  { c.stdin = r }
func (c *restoreVersionCommand) SetStdout(w io.Writer) { c.stdout = w }
func (c *restoreVersionCommand) SetStderr(w io.Writer) { c.stderr = w }

// execRestoreVersion returns a tea.Exec command that suspends the TUI and runs the restore.
func (m model) execRestoreVersion(key, versionID string) (tea.Cmd, error) {
	if m.restoreVersionFn == nil {
		return nil, fmt.Errorf("version restore not available")
	}
	raw := fmt.Sprintf("%s://%s/%s", m.scheme, m.bucket, key)
	u, err := uri.Parse(raw)
	if err != nil {
		return nil, err
	}
	c := &restoreVersionCommand{restoreVersionFn: m.restoreVersionFn, uri: u, versionID: versionID}
	return tea.Exec(c, func(err error) tea.Msg {
		return editDoneMsg{err: err, message: c.message}
	}), nil
}

// execEdit returns a tea.Exec command that suspends the TUI and runs the edit.
func (m model) execEdit(key string) (tea.Cmd, error) {
	raw := fmt.Sprintf("%s://%s/%s", m.scheme, m.bucket, key)
	u, err := uri.Parse(raw)
	if err != nil {
		return nil, err
	}
	c := &editCommand{editFn: m.editFn, uri: u}
	return tea.Exec(c, func(err error) tea.Msg {
		return editDoneMsg{err: err, message: c.message}
	}), nil
}

// execNewFile suspends the TUI and runs the new-file workflow (editor on an
// empty buffer, then upload). choice is the value picked from the choice popup
// (empty when no choices are configured).
func (m model) execNewFile(key, choice string) (tea.Cmd, error) {
	if m.newFileFn == nil {
		return nil, fmt.Errorf("new file creation not available")
	}
	raw := fmt.Sprintf("%s://%s/%s", m.scheme, m.bucket, key)
	u, err := uri.Parse(raw)
	if err != nil {
		return nil, err
	}
	c := &newFileCommand{newFileFn: m.newFileFn, uri: u, choice: choice}
	return tea.Exec(c, func(err error) tea.Msg {
		return newFileDoneMsg{err: err, message: c.message}
	}), nil
}

// newFileCommand implements tea.ExecCommand to run the new-file workflow while
// the TUI is suspended.
type newFileCommand struct {
	newFileFn NewFileFunc
	uri       *uri.URI
	choice    string
	message   string
	stdin     io.Reader
	stdout    io.Writer
	stderr    io.Writer
}

func (c *newFileCommand) Run() error {
	msg, err := c.newFileFn(c.uri, c.choice)
	c.message = msg
	return err
}
func (c *newFileCommand) SetStdin(r io.Reader)  { c.stdin = r }
func (c *newFileCommand) SetStdout(w io.Writer) { c.stdout = w }
func (c *newFileCommand) SetStderr(w io.Writer) { c.stderr = w }

// reloadView rebuilds the current directory's listing (e.g. after creating a
// new object) and returns it as a navigatedMsg.
func (m model) reloadView() tea.Cmd {
	b := m.browser
	ctx := m.ctx
	bucket := m.bucket
	prefix := m.prefix
	return func() tea.Msg {
		nodes, header, canGoUp, err := b.buildViewFor(ctx, bucket, prefix)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		return navigatedMsg{nodes: nodes, header: header, canGoUp: canGoUp, bucket: bucket, prefix: prefix}
	}
}

// navigateUp computes the parent and rebuilds the view.
func (m model) navigateUp() tea.Cmd {
	b := m.browser
	ctx := m.ctx
	bucket := m.bucket
	prefix := m.prefix
	return func() tea.Msg {
		newBucket, newPrefix := b.computeUp(bucket, prefix)
		nodes, header, canGoUp, err := b.buildViewFor(ctx, newBucket, newPrefix)
		return navigatedMsg{nodes: nodes, header: header, canGoUp: canGoUp, bucket: newBucket, prefix: newPrefix, err: err}
	}
}

// navigateToBucket rebuilds the view for the given bucket.
func (m model) navigateToBucket(bucket string) tea.Cmd {
	b := m.browser
	ctx := m.ctx
	return func() tea.Msg {
		nodes, header, canGoUp, err := b.buildViewFor(ctx, bucket, "")
		return navigatedMsg{nodes: nodes, header: header, canGoUp: canGoUp, bucket: bucket, prefix: "", err: err}
	}
}

func (m model) handleChildrenLoaded(msg childrenLoadedMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.message = apierror.Classify(msg.err).Error()
		m.messageIsError = true
		return m, nil
	}
	for _, n := range m.allNodes() {
		if n.entry.key == msg.parentKey && n.entry.isDir {
			n.children = msg.children
			n.loaded = true
			n.expanded = true
			break
		}
	}
	return m, nil
}

// allNodes returns all nodes in the tree (recursive).
func (m model) allNodes() []*node {
	var all []*node
	var walk func(nodes []*node)
	walk = func(nodes []*node) {
		for _, n := range nodes {
			all = append(all, n)
			walk(n.children)
		}
	}
	walk(m.nodes)
	return all
}

// visibleNodes returns a flat list of visible nodes.
// nil entries represent the ".." go-up item.
func (m model) visibleNodes() []*node {
	filter := strings.ToLower(m.filterText)

	var visible []*node
	var walk func(nodes []*node) bool
	walk = func(nodes []*node) bool {
		anyAdded := false
		for _, n := range nodes {
			if filter == "" {
				visible = append(visible, n)
				if n.expanded {
					walk(n.children)
				}
				anyAdded = true
				continue
			}
			nameMatch := strings.Contains(strings.ToLower(n.entry.name), filter)
			if n.entry.isDir && n.expanded {
				pos := len(visible)
				visible = append(visible, n)
				childAdded := walk(n.children)
				if nameMatch || childAdded {
					anyAdded = true
				} else {
					visible = visible[:pos]
				}
			} else if nameMatch {
				visible = append(visible, n)
				anyAdded = true
			}
		}
		return anyAdded
	}
	walk(m.nodes)

	if m.canGoUp {
		visible = append(visible, nil) // nil = ".."
	}
	return visible
}

// revealDir opens a directory node while a confirmed filter is active: it clears
// the filter so the directory's full contents show, ensures the node is (or gets)
// expanded, and keeps the cursor on the directory in the now-unfiltered list.
func (m model) revealDir(n *node) (model, tea.Cmd) {
	m.filtering = false
	m.filterText = ""
	if !n.loaded {
		m.cursor = m.visibleIndexOf(n)
		m.loading = true
		return m, tea.Batch(m.startLoading(), m.loadChildren(n))
	}
	n.expanded = true
	m.cursor = m.visibleIndexOf(n)
	return m, nil
}

// visibleIndexOf returns the index of target in the current visible list, or 0
// if it is not found.
func (m model) visibleIndexOf(target *node) int {
	for i, n := range m.visibleNodes() {
		if n == target {
			return i
		}
	}
	return 0
}

// findParent finds the nearest ancestor directory node for the item at cursorIdx.
func (m model) findParent(visible []*node, cursorIdx int) (*node, int) {
	target := visible[cursorIdx]
	if target == nil {
		return nil, -1
	}
	for i := cursorIdx - 1; i >= 0; i-- {
		n := visible[i]
		if n != nil && n.entry.isDir && n.depth < target.depth {
			return n, i
		}
	}
	return nil, -1
}

// startLoading returns a tick command to start the spinner.
// Caller must set m.loading = true before calling this.
func (m model) startLoading() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m model) loadChildren(n *node) tea.Cmd {
	key := n.entry.key
	depth := n.depth
	bucket := m.bucket
	client := m.client
	ctx := m.ctx

	return func() tea.Msg {
		entries, err := client.List(ctx, bucket, key, "/")
		if err != nil {
			return childrenLoadedMsg{parentKey: key, err: err}
		}
		var children []*node
		for _, e := range entries {
			name := strings.TrimPrefix(e.Key, key)
			if name == "" {
				continue
			}
			children = append(children, &node{
				entry: entry{
					name:         name,
					key:          e.Key,
					isDir:        e.IsDir,
					size:         e.Size,
					lastModified: e.LastModified,
				},
				depth: depth + 1,
			})
		}
		return childrenLoadedMsg{parentKey: key, children: children}
	}
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	// Header art
	b.WriteString("\n")
	b.WriteString("      ██  _   ___  _    ____\n")
	b.WriteString("     ██  /_\\ | _ \\| |  | __|\n")
	b.WriteString("  ▄▄██▄ / _ \\| | || |__| _|\n")
	b.WriteString("  ██████_/ \\_\\___/|____|____|\n")
	b.WriteString("   ▀██▀  " + styleMeta.Render(m.version) + "\n")
	b.WriteString("\n")

	// Path
	b.WriteString("  " + styleHeader.Render(m.header) + "\n\n")

	visible := m.visibleNodes()

	// Determine visible range for scrolling.
	// Header uses 9 lines (art + path + blanks), help uses 3 lines.
	const headerLines = 9
	const footerLines = 3
	listHeight := len(visible)
	startIdx := 0
	endIdx := listHeight
	if m.termHeight > 0 {
		maxItems := m.termHeight - headerLines - footerLines
		if m.message != "" {
			maxItems -= 2 // message line + blank
		}
		if maxItems < 1 {
			maxItems = 1
		}
		if listHeight > maxItems {
			half := maxItems / 2
			startIdx = m.cursor - half
			if startIdx < 0 {
				startIdx = 0
			}
			endIdx = startIdx + maxItems
			if endIdx > listHeight {
				endIdx = listHeight
				startIdx = endIdx - maxItems
			}
		}
	}

	// Calculate max name column width for alignment.
	// All emoji icons render as 2 cells in terminal, but runewidth
	// misreports some (e.g. 🖼️), so we use a constant.
	const iconDisplayWidth = 2
	maxNameWidth := 0
	for i := startIdx; i < endIdx; i++ {
		n := visible[i]
		if n == nil || n.entry.isDir || n.entry.isBucket {
			continue
		}
		w := n.depth*4 + iconDisplayWidth + 1 + len(n.entry.name)
		if w > maxNameWidth {
			maxNameWidth = w
		}
	}

	if startIdx > 0 {
		b.WriteString(styleHelp.Render(fmt.Sprintf("  (%d more above)", startIdx)) + "\n")
	}

	for i := startIdx; i < endIdx; i++ {
		n := visible[i]
		isCursor := i == m.cursor
		prefix := "  "
		if isCursor {
			prefix = styleCursor.Render("> ")
		}

		if n == nil {
			// ".." entry
			line := ".."
			if isCursor {
				line = styleSelected.Render(line)
			}
			b.WriteString(prefix + line + "\n")
			continue
		}

		indent := strings.Repeat("    ", n.depth)
		icon := iconForEntry(n.entry.name, n.entry.isDir, n.entry.isBucket)
		nameCol := indent + icon + " " + n.entry.name

		// Build metadata suffix for files, aligned to a common column
		var metaSuffix string
		if !n.entry.isDir && !n.entry.isBucket {
			nameWidth := n.depth*4 + iconDisplayWidth + 1 + len(n.entry.name)
			pad := ""
			if maxNameWidth > nameWidth {
				pad = strings.Repeat(" ", maxNameWidth-nameWidth)
			}
			var metaParts []string
			if n.entry.size > 0 {
				s := formatSize(n.entry.size)
				metaParts = append(metaParts, fmt.Sprintf("%10s", s))
			}
			if !n.entry.lastModified.IsZero() {
				metaParts = append(metaParts, n.entry.lastModified.Format("2006-01-02 15:04"))
			}
			if len(metaParts) > 0 {
				metaSuffix = pad + "  " + styleMeta.Render(strings.Join(metaParts, "  "))
			}
		}

		if isCursor {
			nameCol = styleSelected.Render(nameCol)
		}
		b.WriteString(prefix + nameCol + metaSuffix + "\n")
	}

	if endIdx < listHeight {
		b.WriteString(styleHelp.Render(fmt.Sprintf("  (%d more below)", listHeight-endIdx)) + "\n")
	}

	// Message (e.g. error from last action)
	if m.message != "" {
		msgStyle := styleMessageSuccess
		if m.messageIsError {
			msgStyle = styleMessageError
		}
		b.WriteString("\n  " + msgStyle.Render(m.message) + "\n")
	}

	// Context menu
	if m.menuOpen && m.menuTarget != nil {
		b.WriteString("\n")
		var menuBuf strings.Builder
		menuBuf.WriteString(styleMeta.Render(m.menuTarget.entry.name) + "\n")
		for i, item := range menuItems {
			prefix := "  "
			if i == m.menuCursor {
				prefix = styleCursor.Render("> ")
				menuBuf.WriteString(prefix + styleMenuSelected.Render(item.label) + "\n")
			} else {
				menuBuf.WriteString(prefix + styleMenuItem.Render(item.label) + "\n")
			}
		}
		b.WriteString(styleMenuBorder.Render(menuBuf.String()))
		b.WriteString("\n")
	}

	// New-file choice popup
	if m.newFileChoosing {
		b.WriteString("\n")
		var choiceBuf strings.Builder
		title := m.newFileChoiceTitle
		if title == "" {
			title = "Select"
		}
		choiceBuf.WriteString(styleMeta.Render(title+": "+m.newFileName) + "\n")
		for i, c := range m.newFileChoices {
			if i == m.newFileChoiceCursor {
				choiceBuf.WriteString(styleCursor.Render("> ") + styleMenuSelected.Render(c) + "\n")
			} else {
				choiceBuf.WriteString("  " + styleMenuItem.Render(c) + "\n")
			}
		}
		b.WriteString(styleMenuBorder.Render(choiceBuf.String()))
		b.WriteString("\n")
	}

	// Version mode
	if m.versionMode && len(m.versionList) > 0 {
		b.WriteString("\n")
		b.WriteString(m.renderVersionPane())
		b.WriteString("\n")
	}

	// Confirm dialog
	if m.confirmMode {
		b.WriteString("\n  " + styleInput.Render(m.confirmPrompt) + "\n")
	}

	// Input line. A block caret (reverse video + blink) covers the character
	// under inputCursor; at end-of-line it covers a trailing space.
	if m.inputMode {
		runes := []rune(m.inputText)
		cur := m.inputCursor
		if cur > len(runes) {
			cur = len(runes)
		}
		before := string(runes[:cur])
		caretCh := " "
		after := ""
		if cur < len(runes) {
			caretCh = string(runes[cur])
			after = string(runes[cur+1:])
		}
		line := styleInput.Render(m.inputPrompt+": "+before) +
			styleInputCursor.Render(caretCh) +
			styleInput.Render(after)
		b.WriteString("\n  " + line + "\n")
		if len(m.inputCandidates) > 0 {
			b.WriteString(renderCandidates(m.inputCandidates, m.inputCandidateCursor))
		}
	}

	// Filter line
	if m.filtering {
		b.WriteString("\n  " + styleFilter.Render("/ "+m.filterText) + "▏\n")
	}

	// Help text
	b.WriteString("\n")
	if m.loading {
		frame := spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
		b.WriteString(styleHelp.Render("  "+frame+" Loading...") + "\n")
	}

	var help string
	if m.confirmMode {
		help = "  y confirm  N/esc cancel"
	} else if m.versionMode {
		help = "  ↑/↓ navigate  C-d/C-u scroll  enter restore  esc close"
	} else if m.menuOpen {
		help = "  ↑/↓ navigate  enter select  esc/← close"
	} else if m.newFileChoosing {
		help = "  ↑/↓ navigate  enter select  esc cancel"
	} else if m.inputMode {
		if len(m.inputCandidates) > 0 {
			help = "  tab ↑/↓ ←/→ select  enter pick  esc close list"
		} else {
			help = "  tab complete  ^a/^e move  enter confirm  esc cancel"
		}
	} else {
		help = "  ↑/↓ navigate  ←/→ collapse/expand  enter select  → menu"
		if m.canGoUp {
			help += "  - up"
		}
		if m.newFileFn != nil {
			help += "  n new"
		}
		help += "  / filter  esc×2 quit"
	}
	b.WriteString(styleHelp.Render(help) + "\n")

	return b.String()
}

// versionPaneHeight returns the number of rows available for the version pane content.
// This is the full available height regardless of version count, so the preview
// pane always has enough space even when there are only a few versions.
// Header art(9) + path(2) + version border(2) + help(3) + message(2 worst case) = ~18 overhead lines.
func (m model) versionPaneHeight() int {
	const overhead = 18
	if m.termHeight <= overhead {
		return len(m.versionList)
	}
	max := m.termHeight - overhead
	if max < 3 {
		max = 3
	}
	return max
}

// renderVersionPane renders the version list with an optional preview pane.
func (m model) renderVersionPane() string {
	targetName := ""
	if m.versionTarget != nil {
		targetName = m.versionTarget.entry.name
	}

	// Determine pane height and how many version items to display
	paneHeight := m.versionPaneHeight()
	total := len(m.versionList)
	maxItems := paneHeight
	if maxItems > total {
		maxItems = total
	}

	// Scroll the version list to keep cursor visible
	startIdx := 0
	if total > maxItems {
		half := maxItems / 2
		startIdx = m.versionCursor - half
		if startIdx < 0 {
			startIdx = 0
		}
		if startIdx+maxItems > total {
			startIdx = total - maxItems
		}
	}
	endIdx := startIdx + maxItems
	if endIdx > total {
		endIdx = total
	}

	// Build the version list (left pane)
	var verBuf strings.Builder
	verBuf.WriteString(styleMeta.Render("Versions: "+targetName) + "\n")

	if startIdx > 0 {
		verBuf.WriteString(styleHelp.Render(fmt.Sprintf("  (%d more above)", startIdx)) + "\n")
	}

	for i := startIdx; i < endIdx; i++ {
		v := m.versionList[i]
		prefix := "  "
		if i == m.versionCursor {
			prefix = styleCursor.Render("> ")
		}

		vid := v.VersionID
		if len(vid) > 12 {
			vid = vid[:12]
		}
		var parts []string
		parts = append(parts, fmt.Sprintf("%-12s", vid))
		if !v.LastModified.IsZero() {
			parts = append(parts, v.LastModified.Format("2006-01-02 15:04"))
		}
		if !v.IsDeleteMarker {
			parts = append(parts, formatSize(v.Size))
		}
		if v.IsLatest {
			parts = append(parts, "(current)")
		}
		if v.IsDeleteMarker {
			parts = append(parts, "[delete marker]")
		}
		label := strings.Join(parts, "  ")

		if i == m.versionCursor {
			verBuf.WriteString(prefix + styleMenuSelected.Render(label) + "\n")
		} else {
			verBuf.WriteString(prefix + styleMenuItem.Render(label) + "\n")
		}
	}

	if endIdx < total {
		verBuf.WriteString(styleHelp.Render(fmt.Sprintf("  (%d more below)", total-endIdx)) + "\n")
	}

	leftContent := verBuf.String()

	// If terminal is too narrow, skip preview
	const minWidthForPreview = 60
	if m.termWidth > 0 && m.termWidth < minWidthForPreview {
		return styleMenuBorder.Render(leftContent)
	}

	// Calculate widths
	leftWidth := 40  // default minimum
	rightWidth := 40 // default minimum
	if m.termWidth > 0 {
		leftWidth = m.termWidth * 40 / 100
		if leftWidth < 30 {
			leftWidth = 30
		}
		rightWidth = m.termWidth - leftWidth - 6 // account for borders and gap
		if rightWidth < 20 {
			rightWidth = 20
		}
	}

	// Use paneHeight for consistent layout regardless of version count.
	// +1 for the "Versions: ..." header line.
	leftLines := paneHeight + 1

	// Pad left pane if fewer versions than pane height
	padLines := paneHeight - (endIdx - startIdx)
	if startIdx > 0 {
		padLines-- // "more above" takes a line
	}
	if endIdx < total {
		padLines-- // "more below" takes a line
	}
	for i := 0; i < padLines; i++ {
		verBuf.WriteString("\n")
	}

	// Build preview pane (right pane) with height matching left pane
	var prevBuf strings.Builder
	prevBuf.WriteString(styleMeta.Render("Preview") + "\n")
	contentLines := leftLines - 1 // subtract "Preview" header

	if m.previewLoading {
		prevBuf.WriteString(styleMeta.Render("Loading...") + "\n")
		contentLines--
	} else if m.previewError != "" {
		prevBuf.WriteString(styleMessageError.Render(m.previewError) + "\n")
		contentLines--
	} else if m.previewContent != "" {
		lines := strings.Split(m.previewContent, "\n")
		start := m.previewScroll
		if start > len(lines) {
			start = len(lines)
		}
		end := start + contentLines
		if end > len(lines) {
			end = len(lines)
		}
		for _, line := range lines[start:end] {
			// Truncate long lines to fit the right pane width
			if len(line) > rightWidth-2 {
				line = line[:rightWidth-2]
			}
			prevBuf.WriteString(line + "\n")
		}
		contentLines -= (end - start)
		if end < len(lines) && contentLines > 0 {
			prevBuf.WriteString(styleMeta.Render(fmt.Sprintf("(%d more lines)", len(lines)-end)) + "\n")
			contentLines--
		}
	} else {
		prevBuf.WriteString(styleMeta.Render("(no content)") + "\n")
		contentLines--
	}

	// Pad remaining lines to match left pane height
	for contentLines > 0 {
		prevBuf.WriteString("\n")
		contentLines--
	}

	rightContent := prevBuf.String()

	leftPane := styleMenuBorder.Width(leftWidth).Render(leftContent)
	rightPane := stylePreviewBorder.Width(rightWidth).Render(rightContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, "  ", rightPane)
}
