package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/acm-gaming/beammp-deploy/internal/config"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

var errFlowCanceled = errors.New("interactive flow canceled")

type configAction int

const (
	actionAddServer configAction = iota
	actionEditServer
	actionRemoveServer
	actionAddModule
	actionEditModule
	actionRemoveModule
	actionSave
	actionExit
)

func addConfigCommands(root *cobra.Command) {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Manage config in an interactive TUI",
		RunE:  runConfigCommand,
	}

	root.AddCommand(configCmd)
}

func runConfigCommand(cmd *cobra.Command, _ []string) error {
	cfg, cfgPath, err := loadConfigForEdit(configPath)
	if err != nil {
		return err
	}

	ui := newConfigUI(cmd)
	dirty := false
	status := ""
	statusErr := false

	for {
		action, err := ui.mainMenu(cfg, dirty, status, statusErr)
		if err != nil {
			if errors.Is(err, errFlowCanceled) {
				action = actionExit
			} else {
				return err
			}
		}

		status = ""
		statusErr = false

		type actionEntry struct {
			fn      func(*configUI, *config.Config) (bool, error)
			success string
		}
		actionMap := map[configAction]actionEntry{
			actionAddServer:    {addServer, "Server added"},
			actionEditServer:   {editServer, "Server updated"},
			actionRemoveServer: {removeServer, "Server removed"},
			actionAddModule:    {addModule, "Module added"},
			actionEditModule:   {editModule, "Module updated"},
			actionRemoveModule: {removeModule, "Module removed"},
		}

		if entry, ok := actionMap[action]; ok {
			changed, err := entry.fn(ui, cfg)
			if err != nil {
				if errors.Is(err, errFlowCanceled) {
					status = "Canceled"
				} else {
					status = err.Error()
					statusErr = true
				}
				continue
			}
			dirty = dirty || changed
			status = entry.success
			continue
		}

		switch action {
		case actionSave:
			if err := saveConfig(cfg, cfgPath); err != nil {
				status = err.Error()
				statusErr = true
				continue
			}
			dirty = false
			status = fmt.Sprintf("Saved config: %s", cfgPath)
		case actionExit:
			if !dirty {
				fmt.Fprintln(cmd.OutOrStdout(), "Exited config editor")
				return nil
			}

			choice, err := ui.selectOption(
				"Unsaved changes",
				"Choose what to do before exiting",
				[]optionItem{
					{title: "Save and exit", description: "Validate and write config"},
					{title: "Discard and exit", description: "Exit without saving"},
					{title: "Back", description: "Return to menu"},
				},
			)
			if err != nil {
				if errors.Is(err, errFlowCanceled) {
					status = "Canceled"
					continue
				}
				return err
			}

			switch choice {
			case 0:
				if err := saveConfig(cfg, cfgPath); err != nil {
					status = err.Error()
					statusErr = true
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Saved config: %s\n", cfgPath)
				return nil
			case 1:
				fmt.Fprintln(cmd.OutOrStdout(), "Exited without saving")
				return nil
			default:
				status = "Save canceled"
			}
		}
	}
}

func loadConfigForEdit(flagPath string) (*config.Config, string, error) {
	cfgPath, err := resolveConfigPath(flagPath)
	if err != nil {
		return nil, "", err
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &config.Config{}, cfgPath, nil
		}
		return nil, "", fmt.Errorf("read config file: %w", err)
	}

	cfg := &config.Config{}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, "", fmt.Errorf("parse config TOML: %w", err)
	}
	return cfg, cfgPath, nil
}

func resolveConfigPath(flagPath string) (string, error) {
	if strings.TrimSpace(flagPath) != "" {
		return flagPath, nil
	}
	return config.DefaultPath()
}

func saveConfig(cfg *config.Config, path string) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config TOML: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	return nil
}

func addServer(ui *configUI, cfg *config.Config) (bool, error) {
	values, err := ui.inputFields("Add Server", "Create a new deploy target", []formField{
		{Label: "Server name", Required: true, Placeholder: "my-server"},
		{Label: "SSH target", Required: true, Placeholder: "root@192.168.1.1"},
		{Label: "SSH key path", Placeholder: "~/.ssh/id_rsa (optional)"},
		{Label: "Remote root path", Required: true, Placeholder: "/var/lib/server/1"},
	})
	if err != nil {
		return false, err
	}

	ok, err := ui.confirm("Confirm", "Add this server?", []string{
		"Name: " + values[0],
		"SSH: " + values[1],
		"SSH key: " + blankAsUnset(values[2]),
		"Remote path: " + values[3],
	})
	if err != nil {
		return false, err
	}
	if !ok {
		return false, errFlowCanceled
	}

	cfg.Servers = append(cfg.Servers, config.Server{
		Name:   values[0],
		SSH:    values[1],
		SSHKey: values[2],
		Path:   values[3],
	})
	return true, nil
}

func addModule(ui *configUI, cfg *config.Config) (bool, error) {
	if len(cfg.Servers) == 0 {
		return false, errors.New("no servers configured; add a server first")
	}

	serverIdx, err := selectServer(ui, cfg.Servers)
	if err != nil {
		return false, err
	}

	values, err := ui.inputFields("Add Module", "Attach a module to the selected server", []formField{
		{Label: "Module name", Required: true, Placeholder: "beammp-mod"},
		{Label: "Repository URL", Placeholder: "https://github.com/org/repo (or leave blank for local)"},
		{Label: "Local path", Placeholder: "~/mods/beammp-mod (or leave blank for repository)"},
		{Label: "Branch", Placeholder: "main (optional; required if repo has no releases)"},
		{Label: "Module path", Placeholder: "subdir (optional)"},
	})
	if err != nil {
		return false, err
	}

	ok, err := ui.confirm("Confirm", "Add this module?", []string{
		"Server: " + cfg.Servers[serverIdx].Name,
		"Name: " + values[0],
		"Repository: " + values[1],
		"Local: " + blankAsUnset(values[2]),
		"Branch: " + blankAsUnset(values[3]),
		"Path: " + blankAsUnset(values[4]),
	})
	if err != nil {
		return false, err
	}
	if !ok {
		return false, errFlowCanceled
	}

	cfg.Servers[serverIdx].Modules = append(cfg.Servers[serverIdx].Modules, config.Module{
		Name:       values[0],
		Repository: values[1],
		Local:      values[2],
		Branch:     values[3],
		Path:       values[4],
	})
	return true, nil
}

func editServer(ui *configUI, cfg *config.Config) (bool, error) {
	if len(cfg.Servers) == 0 {
		return false, errors.New("no servers configured")
	}

	serverIdx, err := selectServer(ui, cfg.Servers)
	if err != nil {
		return false, err
	}

	s := cfg.Servers[serverIdx]
	values, err := ui.inputFields("Edit Server", "Update server details", []formField{
		{Label: "Server name", Value: s.Name, Required: true},
		{Label: "SSH target", Value: s.SSH, Required: true},
		{Label: "SSH key path", Value: s.SSHKey},
		{Label: "Remote root path", Value: s.Path, Required: true},
	})
	if err != nil {
		return false, err
	}

	ok, err := ui.confirm("Confirm", "Apply server changes?", []string{
		"Server: " + s.Name,
		"New name: " + values[0],
		"SSH: " + values[1],
		"SSH key: " + blankAsUnset(values[2]),
		"Remote path: " + values[3],
	})
	if err != nil {
		return false, err
	}
	if !ok {
		return false, errFlowCanceled
	}

	cfg.Servers[serverIdx].Name = values[0]
	cfg.Servers[serverIdx].SSH = values[1]
	cfg.Servers[serverIdx].SSHKey = values[2]
	cfg.Servers[serverIdx].Path = values[3]
	return true, nil
}

func editModule(ui *configUI, cfg *config.Config) (bool, error) {
	if len(cfg.Servers) == 0 {
		return false, errors.New("no servers configured")
	}

	serverIdx, err := selectServer(ui, cfg.Servers)
	if err != nil {
		return false, err
	}

	if len(cfg.Servers[serverIdx].Modules) == 0 {
		return false, fmt.Errorf("server %q has no modules", cfg.Servers[serverIdx].Name)
	}

	moduleIdx, err := selectModule(ui, cfg.Servers[serverIdx].Modules)
	if err != nil {
		return false, err
	}

	m := cfg.Servers[serverIdx].Modules[moduleIdx]
	values, err := ui.inputFields("Edit Module", "Update module settings", []formField{
		{Label: "Module name", Value: m.Name, Required: true},
		{Label: "Repository URL", Value: m.Repository},
		{Label: "Local path", Value: m.Local},
		{Label: "Branch", Value: m.Branch},
		{Label: "Module path", Value: m.Path},
	})
	if err != nil {
		return false, err
	}

	ok, err := ui.confirm("Confirm", "Apply module changes?", []string{
		"Server: " + cfg.Servers[serverIdx].Name,
		"Module: " + m.Name,
		"New name: " + values[0],
		"Repository: " + values[1],
		"Local: " + blankAsUnset(values[2]),
		"Branch: " + blankAsUnset(values[3]),
		"Path: " + blankAsUnset(values[4]),
	})
	if err != nil {
		return false, err
	}
	if !ok {
		return false, errFlowCanceled
	}

	cfg.Servers[serverIdx].Modules[moduleIdx].Name = values[0]
	cfg.Servers[serverIdx].Modules[moduleIdx].Repository = values[1]
	cfg.Servers[serverIdx].Modules[moduleIdx].Local = values[2]
	cfg.Servers[serverIdx].Modules[moduleIdx].Branch = values[3]
	cfg.Servers[serverIdx].Modules[moduleIdx].Path = values[4]
	return true, nil
}

func removeServer(ui *configUI, cfg *config.Config) (bool, error) {
	if len(cfg.Servers) == 0 {
		return false, errors.New("no servers configured")
	}

	serverIdx, err := selectServer(ui, cfg.Servers)
	if err != nil {
		return false, err
	}

	s := cfg.Servers[serverIdx]
	ok, err := ui.confirm("Danger", "Remove this server and all its modules?", []string{
		"Name: " + s.Name,
		"SSH: " + s.SSH,
		fmt.Sprintf("Modules: %d", len(s.Modules)),
	})
	if err != nil {
		return false, err
	}
	if !ok {
		return false, errFlowCanceled
	}

	cfg.Servers = append(cfg.Servers[:serverIdx], cfg.Servers[serverIdx+1:]...)
	return true, nil
}

func removeModule(ui *configUI, cfg *config.Config) (bool, error) {
	if len(cfg.Servers) == 0 {
		return false, errors.New("no servers configured")
	}

	serverIdx, err := selectServer(ui, cfg.Servers)
	if err != nil {
		return false, err
	}

	modules := cfg.Servers[serverIdx].Modules
	if len(modules) == 0 {
		return false, fmt.Errorf("server %q has no modules", cfg.Servers[serverIdx].Name)
	}

	moduleIdx, err := selectModule(ui, modules)
	if err != nil {
		return false, err
	}

	m := modules[moduleIdx]
	ok, err := ui.confirm("Danger", "Remove this module?", []string{
		"Server: " + cfg.Servers[serverIdx].Name,
		"Module: " + m.Name,
	})
	if err != nil {
		return false, err
	}
	if !ok {
		return false, errFlowCanceled
	}

	cfg.Servers[serverIdx].Modules = append(modules[:moduleIdx], modules[moduleIdx+1:]...)
	return true, nil
}

func selectServer(ui *configUI, servers []config.Server) (int, error) {
	items := make([]optionItem, 0, len(servers))
	for _, s := range servers {
		items = append(items, optionItem{
			title:       s.Name,
			description: fmt.Sprintf("%s | %d module(s)", s.SSH, len(s.Modules)),
		})
	}
	return ui.selectOption("Select Server", "Choose a server", items)
}

func selectModule(ui *configUI, modules []config.Module) (int, error) {
	items := make([]optionItem, 0, len(modules))
	for _, m := range modules {
		source := m.Repository
		if strings.TrimSpace(m.Local) != "" {
			source = "local: " + m.Local
		}
		if strings.TrimSpace(source) == "" {
			source = "(unset)"
		}
		branch := blankAsUnset(m.Branch)
		items = append(items, optionItem{
			title:       m.Name,
			description: fmt.Sprintf("%s | branch %s", source, branch),
		})
	}
	return ui.selectOption("Select Module", "Choose a module", items)
}

func blankAsUnset(v string) string {
	if strings.TrimSpace(v) == "" {
		return "(unset)"
	}
	return v
}

type configUI struct {
	in  io.Reader
	out io.Writer
}

func newConfigUI(cmd *cobra.Command) *configUI {
	return &configUI{in: cmd.InOrStdin(), out: cmd.OutOrStdout()}
}

func (u *configUI) mainMenu(cfg *config.Config, dirty bool, status string, statusErr bool) (configAction, error) {
	moduleCount := 0
	for _, s := range cfg.Servers {
		moduleCount += len(s.Modules)
	}

	title := "BeamMP Config"
	subtitle := fmt.Sprintf("Servers: %d | Modules: %d", len(cfg.Servers), moduleCount)
	if dirty {
		subtitle += " | UNSAVED"
	}
	if strings.TrimSpace(status) != "" {
		subtitle += "\n"
		if statusErr {
			subtitle += "Error: " + status
		} else {
			subtitle += status
		}
	}

	choice, err := u.selectOption(title, subtitle, []optionItem{
		{title: "Add server", description: "Create a new server entry"},
		{title: "Edit server", description: "Update server fields"},
		{title: "Remove server", description: "Delete server and nested modules"},
		{title: "Add module", description: "Add module to a server"},
		{title: "Edit module", description: "Update module settings"},
		{title: "Remove module", description: "Delete module from a server"},
		{title: "Save changes", description: "Validate and write config file"},
		{title: "Exit", description: "Leave config editor"},
	})
	if err != nil {
		return actionExit, err
	}
	return configAction(choice), nil
}

func (u *configUI) selectOption(title, subtitle string, options []optionItem) (int, error) {
	if len(options) == 0 {
		return 0, errors.New("no options available")
	}

	model := newListModel(title, subtitle, options)
	finalModel, err := tea.NewProgram(model, tea.WithInput(u.in), tea.WithOutput(u.out)).Run()
	if err != nil {
		return 0, err
	}

	m, ok := finalModel.(*listScreenModel)
	if !ok {
		return 0, errors.New("unexpected list state")
	}
	if m.canceled {
		return 0, errFlowCanceled
	}
	return m.selected, nil
}

func (u *configUI) inputFields(title, subtitle string, fields []formField) ([]string, error) {
	model := newFormModel(title, subtitle, fields)
	finalModel, err := tea.NewProgram(model, tea.WithInput(u.in), tea.WithOutput(u.out)).Run()
	if err != nil {
		return nil, err
	}

	m, ok := finalModel.(*formModel)
	if !ok {
		return nil, errors.New("unexpected form state")
	}
	if m.canceled {
		return nil, errFlowCanceled
	}

	values := make([]string, 0, len(m.inputs))
	for _, in := range m.inputs {
		values = append(values, strings.TrimSpace(in.Value()))
	}
	return values, nil
}

func (u *configUI) confirm(title, subtitle string, lines []string) (bool, error) {
	options := []optionItem{
		{title: "Confirm", description: "Apply change"},
		{title: "Cancel", description: "Go back"},
	}
	combined := subtitle
	if len(lines) > 0 {
		combined += "\n" + strings.Join(lines, "\n")
	}
	selected, err := u.selectOption(title, combined, options)
	if err != nil {
		return false, err
	}
	return selected == 0, nil
}

type optionItem struct {
	title       string
	description string
}

func (o optionItem) Title() string       { return o.title }
func (o optionItem) Description() string { return o.description }
func (o optionItem) FilterValue() string { return o.title + " " + o.description }

type listScreenModel struct {
	title    string
	subtitle string
	list     list.Model
	delegate list.DefaultDelegate
	selected int
	canceled bool
	width    int
	height   int
}

func newListModel(title, subtitle string, options []optionItem) *listScreenModel {
	items := make([]list.Item, 0, len(options))
	for _, option := range options {
		items = append(items, option)
	}

	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = true
	delegate.SetSpacing(1)
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(lipgloss.Color("117")).
		BorderLeftForeground(lipgloss.Color("117")).
		Bold(true)
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(lipgloss.Color("252")).
		BorderLeftForeground(lipgloss.Color("117"))
	delegate.Styles.NormalTitle = delegate.Styles.NormalTitle.Foreground(lipgloss.Color("254"))
	delegate.Styles.NormalDesc = delegate.Styles.NormalDesc.Foreground(lipgloss.Color("245"))

	l := list.New(items, delegate, 96, 20)
	l.Title = ""
	l.SetShowStatusBar(false)
	l.SetShowFilter(false)
	l.SetFilteringEnabled(false)
	l.SetShowPagination(false)
	l.SetShowHelp(false)
	l.Styles.Title = titleStyle
	l.Styles.NoItems = mutedStyle
	l.Styles.HelpStyle = helpStyle

	m := &listScreenModel{
		title:    title,
		subtitle: subtitle,
		list:     l,
		delegate: delegate,
	}
	m.applyListLayout()
	return m
}

func (m *listScreenModel) Init() tea.Cmd { return nil }

func (m *listScreenModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = v.Width
		m.height = v.Height
		m.applyListLayout()
		return m, nil
	case tea.KeyMsg:
		switch v.String() {
		case "ctrl+c", "esc", "q":
			m.canceled = true
			return m, tea.Quit
		case "enter":
			m.selected = m.list.Index()
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m *listScreenModel) View() string {
	cardWidth := responsiveCardWidth(m.width)
	contentWidth := max(24, cardWidth-6)
	centered := lipgloss.NewStyle().Width(contentWidth).Align(lipgloss.Center)

	var body strings.Builder
	body.WriteString(centered.Render(headerBoxStyle.Render(m.title)))
	if strings.TrimSpace(m.subtitle) != "" {
		body.WriteString("\n")
		body.WriteString(centered.Render(subtitleStyle.Render(m.subtitle)))
	}
	body.WriteString("\n\n")
	body.WriteString(panelStyle.Width(contentWidth).Render(m.list.View()))
	body.WriteString("\n")
	body.WriteString(centered.Render(helpStyle.Render("enter select  •  arrows/jk move  •  esc cancel")))

	card := frameStyle.Width(cardWidth).Render(body.String())
	return placeCenter(m.width, m.height, card)
}

func (m *listScreenModel) applyListLayout() {
	cardWidth := responsiveCardWidth(m.width)
	listWidth := clamp(cardWidth-10, 24, 100)
	listHeight := clamp(m.height-14, 8, 26)
	if m.height == 0 {
		listHeight = 18
	}
	m.list.SetWidth(listWidth)
	m.list.SetHeight(listHeight)

	itemWidth := max(16, listWidth-7)
	m.delegate.Styles.NormalTitle = m.delegate.Styles.NormalTitle.Width(itemWidth).Align(lipgloss.Center)
	m.delegate.Styles.NormalDesc = m.delegate.Styles.NormalDesc.Width(itemWidth).Align(lipgloss.Center)
	m.delegate.Styles.SelectedTitle = m.delegate.Styles.SelectedTitle.Width(itemWidth).Align(lipgloss.Center)
	m.delegate.Styles.SelectedDesc = m.delegate.Styles.SelectedDesc.Width(itemWidth).Align(lipgloss.Center)
	m.list.SetDelegate(m.delegate)
}

type formField struct {
	Label       string
	Value       string
	Required    bool
	Placeholder string
}

type formModel struct {
	title    string
	subtitle string
	fields   []formField
	inputs   []textinput.Model
	cursor   int
	errMsg   string
	canceled bool
	width    int
	height   int
}

func newFormModel(title, subtitle string, fields []formField) *formModel {
	inputs := make([]textinput.Model, 0, len(fields))
	copiedFields := make([]formField, 0, len(fields))
	for _, field := range fields {
		ti := textinput.New()
		ti.Prompt = ""
		ti.SetValue(field.Value)
		ti.Placeholder = field.Placeholder
		ti.CharLimit = 256
		ti.Width = 64
		inputs = append(inputs, ti)
		copiedFields = append(copiedFields, field)
	}

	if len(inputs) > 0 {
		inputs[0].Focus()
	}

	m := &formModel{title: title, subtitle: subtitle, fields: copiedFields, inputs: inputs}
	m.resizeInputs()
	return m
}

func (m *formModel) Init() tea.Cmd { return textinput.Blink }

func (m *formModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = v.Width
		m.height = v.Height
		m.resizeInputs()
		return m, nil
	case tea.KeyMsg:
		switch v.String() {
		case "ctrl+c", "esc":
			m.canceled = true
			return m, tea.Quit
		case "up", "k", "shift+tab":
			m.moveCursor(-1)
			m.errMsg = ""
			return m, nil
		case "down", "j", "tab":
			m.moveCursor(1)
			m.errMsg = ""
			return m, nil
		case "enter":
			if err := m.validateCurrent(); err != nil {
				m.errMsg = err.Error()
				return m, nil
			}
			if m.cursor == len(m.inputs)-1 {
				if err := m.validateAll(); err != nil {
					m.errMsg = err.Error()
					return m, nil
				}
				return m, tea.Quit
			}
			m.moveCursor(1)
			m.errMsg = ""
			return m, nil
		}
	}

	cmds := make([]tea.Cmd, 0, len(m.inputs))
	for i := range m.inputs {
		var cmd tea.Cmd
		m.inputs[i], cmd = m.inputs[i].Update(msg)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m *formModel) moveCursor(delta int) {
	if len(m.inputs) == 0 {
		return
	}
	m.inputs[m.cursor].Blur()
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = len(m.inputs) - 1
	}
	if m.cursor >= len(m.inputs) {
		m.cursor = 0
	}
	m.inputs[m.cursor].Focus()
}

func (m *formModel) validateCurrent() error {
	if len(m.fields) == 0 {
		return nil
	}
	f := m.fields[m.cursor]
	if f.Required && strings.TrimSpace(m.inputs[m.cursor].Value()) == "" {
		return fmt.Errorf("%s is required", strings.ToLower(f.Label))
	}
	return nil
}

func (m *formModel) validateAll() error {
	for i, field := range m.fields {
		if field.Required && strings.TrimSpace(m.inputs[i].Value()) == "" {
			return fmt.Errorf("%s is required", strings.ToLower(field.Label))
		}
	}
	return nil
}

func (m *formModel) View() string {
	cardWidth := responsiveCardWidth(m.width)
	contentWidth := max(24, cardWidth-6)
	inputBoxWidth := clamp(cardWidth-10, 30, 90)
	centered := lipgloss.NewStyle().Width(contentWidth).Align(lipgloss.Center)

	var body strings.Builder
	body.WriteString(centered.Render(headerBoxStyle.Render(m.title)))
	if strings.TrimSpace(m.subtitle) != "" {
		body.WriteString("\n")
		body.WriteString(centered.Render(subtitleStyle.Render(m.subtitle)))
	}
	body.WriteString("\n\n")

	for i, field := range m.fields {
		label := field.Label
		if field.Required {
			label += " *"
		}

		labelStyle := mutedStyle
		if i == m.cursor {
			labelStyle = focusLabelStyle
		}
		body.WriteString(centered.Render(labelStyle.Render(label)))
		body.WriteString("\n")
		body.WriteString(centered.Render(inputBoxStyle.Width(inputBoxWidth).Render(m.inputs[i].View())))
		body.WriteString("\n\n")
	}

	if strings.TrimSpace(m.errMsg) != "" {
		body.WriteString(centered.Render(errorStyle.Render("Error: " + m.errMsg)))
		body.WriteString("\n")
	}

	body.WriteString(centered.Render(helpStyle.Render("type to edit  •  tab/jk move  •  enter next/submit  •  esc cancel")))
	card := frameStyle.Width(cardWidth).Render(body.String())
	return placeCenter(m.width, m.height, card)
}

func (m *formModel) resizeInputs() {
	inputWidth := clamp(responsiveCardWidth(m.width)-14, 26, 84)
	for i := range m.inputs {
		m.inputs[i].Width = inputWidth
	}
}

func clamp(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func responsiveCardWidth(windowWidth int) int {
	if windowWidth <= 0 {
		return 96
	}
	return clamp(windowWidth-10, 50, 110)
}

func placeCenter(width, height int, content string) string {
	if width <= 0 || height <= 0 {
		return content
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
}

var (
	frameStyle = lipgloss.NewStyle().
			Padding(1, 2).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62"))

	headerBoxStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("31")).
			Padding(0, 1).
			MarginBottom(1)

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("25")).
			Padding(0, 1)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("250"))

	panelStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("63"))

	inputBoxStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62"))

	focusLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("86"))

	mutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	helpStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	errorStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
)
