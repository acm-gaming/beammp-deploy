package cmd

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/acm-gaming/beammp-deploy/internal/deploy"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type deployDoneMsg struct {
	err      error
	executed int
}

type deployLogMsg struct {
	line string
}

type deployEventMsg struct {
	event deploy.Event
}

type deployTickMsg time.Time

type deployObserver struct {
	program *tea.Program
}

func (o *deployObserver) OnDeployEvent(event deploy.Event) {
	o.program.Send(deployEventMsg{event: event})
}

type deployLogWriter struct {
	program *tea.Program
	mu      sync.Mutex
	buf     strings.Builder
}

func (w *deployLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buf.Write(p)
	text := w.buf.String()
	lastNewline := strings.LastIndexByte(text, '\n')
	if lastNewline == -1 {
		return len(p), nil
	}

	lines := strings.Split(text[:lastNewline], "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		w.program.Send(deployLogMsg{line: line})
	}

	w.buf.Reset()
	_, _ = w.buf.WriteString(text[lastNewline+1:])
	return len(p), nil
}

func (w *deployLogWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()

	line := strings.TrimSpace(w.buf.String())
	if line != "" {
		w.program.Send(deployLogMsg{line: line})
	}
	w.buf.Reset()
}

type deployModel struct {
	startedAt  time.Time
	configPath string
	cancelFn   func()

	width  int
	height int

	serverTotal       int
	serverIndex       int
	serverName        string
	serverModuleIdx   int
	serverModuleTotal int
	moduleName        string
	totalModules      int
	doneModules       int

	status    string
	canceling bool
	done      bool
	finalErr  error

	logs []string
}

func newDeployProgram(configPath string, in io.Reader, out io.Writer, cancelFn func()) (*tea.Program, *deployLogWriter, *deployObserver) {
	model := &deployModel{
		startedAt:  time.Now(),
		configPath: configPath,
		cancelFn:   cancelFn,
		status:     "Preparing deployment",
		logs:       make([]string, 0, 128),
	}

	program := tea.NewProgram(model, tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen())
	return program, &deployLogWriter{program: program}, &deployObserver{program: program}
}

func (m *deployModel) Init() tea.Cmd {
	return tickDeployClock()
}

func tickDeployClock() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return deployTickMsg(t)
	})
}

func (m *deployModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.done {
				return m, tea.Quit
			}
			m.canceling = true
			m.status = "Cancel requested, stopping deployment"
			if m.cancelFn != nil {
				m.cancelFn()
			}
		}
	case deployTickMsg:
		if m.done {
			return m, nil
		}
		return m, tickDeployClock()
	case deployEventMsg:
		m.applyEvent(msg.event)
	case deployLogMsg:
		m.logs = append(m.logs, msg.line)
		limit := m.logLimit()
		if len(m.logs) > limit {
			m.logs = m.logs[len(m.logs)-limit:]
		}
	case deployDoneMsg:
		m.done = true
		m.finalErr = msg.err
		m.canceling = false
		if msg.err != nil {
			m.status = "Deployment failed"
			m.logs = append(m.logs, "ERROR: "+msg.err.Error())
			limit := m.logLimit()
			if len(m.logs) > limit {
				m.logs = m.logs[len(m.logs)-limit:]
			}
			return m, nil
		} else {
			m.status = "Deployment complete"
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *deployModel) View() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("45")).Render("BeamMP Deploy")
	elapsed := time.Since(m.startedAt).Round(time.Second)
	progressLabel := fmt.Sprintf("%d/%d modules", m.doneModules, m.totalModules)
	if m.totalModules == 0 {
		progressLabel = "0 modules"
	}

	serverLine := "Server: waiting"
	if m.serverTotal > 0 {
		serverLine = fmt.Sprintf("Server: %d/%d", m.serverIndex, m.serverTotal)
		if m.serverName != "" {
			serverLine += " (" + m.serverName + ")"
		}
	}

	moduleLine := "Module: waiting"
	if m.moduleName != "" {
		moduleLine = fmt.Sprintf("Module: %d/%d (%s)", m.serverModuleIdx, m.serverModuleTotal, m.moduleName)
	}

	bar := renderProgressBar(m.doneModules, m.totalModules, max(20, m.width-18))
	status := "Status: " + m.status
	if m.canceling {
		status = "Status: canceling..."
	}

	lines := []string{
		title,
		"Config: " + m.configPath,
		"Elapsed: " + elapsed.String(),
		serverLine,
		moduleLine,
		"Progress: " + progressLabel,
		bar,
		status,
		"",
		"Logs:",
		m.renderLogs(),
		"",
		m.footerHint(),
	}

	return strings.Join(lines, "\n")
}

func (m *deployModel) renderLogs() string {
	logStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1)

	if len(m.logs) == 0 {
		return logStyle.Render("Waiting for logs...")
	}
	return logStyle.Render(strings.Join(m.logs, "\n"))
}

func (m *deployModel) logLimit() int {
	if m.height <= 18 {
		return 8
	}
	available := m.height - 14
	if available < 8 {
		return 8
	}
	return available
}

func (m *deployModel) applyEvent(event deploy.Event) {
	switch event.Type {
	case deploy.EventRunStarted:
		m.serverTotal = event.ServerTotal
		m.totalModules = event.TotalModules
		m.status = "Starting deployment"
	case deploy.EventServerStarted:
		m.serverIndex = event.ServerIndex
		m.serverTotal = event.ServerTotal
		m.serverName = event.Server
		m.status = "Connecting to " + event.Server
	case deploy.EventServerCompleted:
		m.serverIndex = event.ServerIndex
		m.serverTotal = event.ServerTotal
		m.serverName = event.Server
		m.doneModules = event.CompletedModules
		m.status = "Completed " + event.Server
	case deploy.EventModuleStarted:
		m.serverName = event.Server
		m.moduleName = event.Module
		m.serverModuleIdx = event.ServerModuleIndex
		m.serverModuleTotal = event.ServerModuleTotal
		m.doneModules = event.CompletedModules
		m.totalModules = event.TotalModules
		m.status = "Deploying module " + event.Module
	case deploy.EventModuleSkipped:
		m.serverName = event.Server
		m.moduleName = event.Module
		m.serverModuleIdx = event.ServerModuleIndex
		m.serverModuleTotal = event.ServerModuleTotal
		m.doneModules = event.CompletedModules
		m.totalModules = event.TotalModules
		m.status = "Skipped unchanged module " + event.Module
	case deploy.EventModuleCompleted:
		m.serverName = event.Server
		m.moduleName = event.Module
		m.serverModuleIdx = event.ServerModuleIndex
		m.serverModuleTotal = event.ServerModuleTotal
		m.doneModules = event.CompletedModules
		m.totalModules = event.TotalModules
		m.status = "Uploaded module " + event.Module
	case deploy.EventRunCompleted:
		m.doneModules = event.CompletedModules
		m.totalModules = event.TotalModules
		m.status = "Saving deployment cache"
	}
}

func (m *deployModel) footerHint() string {
	if m.done && m.finalErr != nil {
		return "Press q or ctrl+c to exit"
	}
	if m.done {
		return "Completed"
	}
	if m.canceling {
		return "Waiting for deployment to stop..."
	}
	return "Press q or ctrl+c to cancel"
}

func renderProgressBar(done, total, width int) string {
	if width < 10 {
		width = 10
	}
	if total <= 0 {
		return "[" + strings.Repeat("-", width-2) + "]"
	}

	ratio := float64(done) / float64(total)
	if ratio > 1 {
		ratio = 1
	}
	fill := int(ratio * float64(width-2))
	if fill < 0 {
		fill = 0
	}
	if fill > width-2 {
		fill = width - 2
	}
	return "[" + strings.Repeat("=", fill) + strings.Repeat("-", (width-2)-fill) + "]"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
