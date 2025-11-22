package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/digitalocean/godo"
	"golang.org/x/oauth2"
)

type TokenSource struct {
	AccessToken string
}

func (t *TokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{
		AccessToken: t.AccessToken,
	}, nil
}

type dropletItem struct {
	droplet *godo.Droplet
}

func (d dropletItem) FilterValue() string {
	return d.droplet.Name
}

func (d dropletItem) Title() string {
	status := d.droplet.Status
	statusColor := lipgloss.Color("46") // green
	statusIcon := "●"
	if status == "off" {
		statusColor = lipgloss.Color("196") // red
		statusIcon = "○"
	} else if status == "new" {
		statusColor = lipgloss.Color("226") // yellow
		statusIcon = "◐"
	} else if status == "archive" {
		statusColor = lipgloss.Color("240") // gray
		statusIcon = "◯"
	}
	statusStyle := lipgloss.NewStyle().Foreground(statusColor).Bold(true)
	return fmt.Sprintf("%s %s", statusStyle.Render(statusIcon), d.droplet.Name)
}

func (d dropletItem) Description() string {
	ip := "No IP"
	if len(d.droplet.Networks.V4) > 0 {
		ip = d.droplet.Networks.V4[0].IPAddress
	}

	// Format size nicely
	sizeParts := strings.Split(d.droplet.SizeSlug, "-")
	sizeDisplay := d.droplet.SizeSlug
	if len(sizeParts) >= 3 {
		sizeDisplay = fmt.Sprintf("%s %s", sizeParts[1], sizeParts[2])
	}

	return fmt.Sprintf("📍 %s  |  💾 %s  |  🌐 %s  |  🆔 %d",
		d.droplet.Region.Slug, sizeDisplay, ip, d.droplet.ID)
}

type model struct {
	list             list.Model
	client           *godo.Client
	creating         bool
	viewingDetails   bool
	confirmDelete    bool
	deleteTargetID   int
	deleteTargetName string
	loading          bool
	spinner          spinner.Model
	selectedDroplet  *godo.Droplet
	nameInput        textinput.Model
	regionInput      textinput.Model
	sizeInput        textinput.Model
	imageInput       textinput.Model
	tagsInput        textinput.Model
	inputIndex       int
	err              error
	successMsg       string
	showHelp         bool
	dropletCount     int
	lastRefresh      time.Time
}

type errMsg error
type dropletsLoadedMsg []godo.Droplet
type dropletCreatedMsg *godo.Droplet
type dropletDeletedMsg struct{}

var (
	// Colors
	primaryColor = lipgloss.Color("39")  // cyan
	successColor = lipgloss.Color("46")  // green
	errorColor   = lipgloss.Color("196") // red
	warningColor = lipgloss.Color("226") // yellow
	mutedColor   = lipgloss.Color("240") // gray
	bgColor      = lipgloss.Color("235") // dark gray
	borderColor  = lipgloss.Color("39")  // cyan

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor).
			Padding(0, 1).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor)

	statusMessageStyle = lipgloss.NewStyle().
				Foreground(successColor).
				Bold(true).
				Padding(0, 1)

	errorMessageStyle = lipgloss.NewStyle().
				Foreground(errorColor).
				Bold(true).
				Padding(0, 1)

	helpStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Italic(true)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Background(bgColor).
			Padding(0, 1)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor).
			Padding(1, 2)

	docStyle = lipgloss.NewStyle().Margin(1, 2)
)

func initialModel(client *godo.Client) model {
	items := []list.Item{}

	l := list.New(items, list.NewDefaultDelegate(), 80, 20)
	l.Title = "🐳 DigitalOcean Droplets"
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(true)
	l.Styles.Title = titleStyle
	l.Styles.PaginationStyle = lipgloss.NewStyle().MarginLeft(2)
	l.Styles.HelpStyle = helpStyle
	l.Styles.StatusBar = statusBarStyle
	l.Styles.StatusEmpty = statusBarStyle

	// Customize list item styles
	l.Styles.NoItems = lipgloss.NewStyle().
		Foreground(mutedColor).
		Italic(true).
		Padding(1, 2)

	nameInput := textinput.New()
	nameInput.Placeholder = "my-droplet"
	nameInput.Focus()
	nameInput.CharLimit = 50
	nameInput.Width = 50
	nameInput.PromptStyle = lipgloss.NewStyle().Foreground(primaryColor)
	nameInput.TextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))

	regionInput := textinput.New()
	regionInput.Placeholder = "nyc3"
	regionInput.CharLimit = 20
	regionInput.Width = 50
	regionInput.PromptStyle = lipgloss.NewStyle().Foreground(primaryColor)
	regionInput.TextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))

	sizeInput := textinput.New()
	sizeInput.Placeholder = "s-1vcpu-1gb"
	sizeInput.CharLimit = 30
	sizeInput.Width = 50
	sizeInput.PromptStyle = lipgloss.NewStyle().Foreground(primaryColor)
	sizeInput.TextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))

	imageInput := textinput.New()
	imageInput.Placeholder = "ubuntu-22-04-x64"
	imageInput.CharLimit = 50
	imageInput.Width = 50
	imageInput.PromptStyle = lipgloss.NewStyle().Foreground(primaryColor)
	imageInput.TextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))

	tagsInput := textinput.New()
	tagsInput.Placeholder = "web,production"
	tagsInput.CharLimit = 100
	tagsInput.Width = 50
	tagsInput.PromptStyle = lipgloss.NewStyle().Foreground(primaryColor)
	tagsInput.TextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(primaryColor)

	return model{
		list:            l,
		client:          client,
		creating:        false,
		viewingDetails:  false,
		confirmDelete:   false,
		loading:         false,
		spinner:         s,
		selectedDroplet: nil,
		nameInput:       nameInput,
		regionInput:     regionInput,
		sizeInput:       sizeInput,
		imageInput:      imageInput,
		tagsInput:       tagsInput,
		inputIndex:      0,
		showHelp:        true,
		dropletCount:    0,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		loadDroplets(m.client),
		tea.EnterAltScreen,
		m.spinner.Tick,
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.confirmDelete {
			return m.updateDeleteConfirmation(msg)
		}

		if m.creating {
			return m.updateCreateForm(msg)
		}

		if m.viewingDetails {
			switch msg.String() {
			case "esc", "enter", "backspace":
				m.viewingDetails = false
				m.selectedDroplet = nil
				return m, nil
			case "ctrl+c", "q":
				return m, tea.Quit
			}
			return m, nil
		}

		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "n", "N":
			m.creating = true
			m.inputIndex = 0
			m.nameInput.Focus()
			m.err = nil
			m.successMsg = ""
			return m, nil
		case "r", "R":
			m.loading = true
			return m, tea.Batch(loadDroplets(m.client), m.spinner.Tick)
		case "d", "D":
			if m.list.SelectedItem() != nil {
				item := m.list.SelectedItem().(dropletItem)
				m.confirmDelete = true
				m.deleteTargetID = item.droplet.ID
				m.deleteTargetName = item.droplet.Name
				return m, nil
			}
		case "enter":
			if m.list.SelectedItem() != nil {
				item := m.list.SelectedItem().(dropletItem)
				m.viewingDetails = true
				m.selectedDroplet = item.droplet
				return m, nil
			}
		}

	case spinner.TickMsg:
		if m.loading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}

	case dropletsLoadedMsg:
		m.loading = false
		items := make([]list.Item, len(msg))
		for i, d := range msg {
			items[i] = dropletItem{droplet: &d}
		}
		cmd = m.list.SetItems(items)
		m.dropletCount = len(msg)
		m.lastRefresh = time.Now()
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)

	case dropletCreatedMsg:
		m.creating = false
		m.successMsg = fmt.Sprintf("✅ Droplet '%s' created successfully! (ID: %d)", msg.Name, msg.ID)
		m.resetInputs()
		cmds = append(cmds, loadDroplets(m.client))
		return m, tea.Batch(cmds...)

	case dropletDeletedMsg:
		m.confirmDelete = false
		m.successMsg = fmt.Sprintf("✅ Droplet '%s' deleted successfully!", m.deleteTargetName)
		m.deleteTargetID = 0
		m.deleteTargetName = ""
		cmds = append(cmds, loadDroplets(m.client))
		return m, tea.Batch(cmds...)

	case errMsg:
		m.err = msg
		m.creating = false
		m.loading = false
		m.confirmDelete = false
		return m, nil

	case tea.WindowSizeMsg:
		h, v := docStyle.GetFrameSize()
		m.list.SetWidth(msg.Width - h)
		m.list.SetHeight(msg.Height - v - 3)
		return m, nil
	}

	if !m.loading {
		m.list, cmd = m.list.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m model) updateDeleteConfirmation(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		return m, deleteDroplet(m.client, m.deleteTargetID)
	case "n", "N", "esc":
		m.confirmDelete = false
		m.deleteTargetID = 0
		m.deleteTargetName = ""
		return m, nil
	}
	return m, nil
}

func (m model) updateCreateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg.String() {
	case "esc":
		m.creating = false
		m.resetInputs()
		m.err = nil
		return m, nil
	case "tab":
		m.inputIndex = (m.inputIndex + 1) % 5
		m.updateInputFocus()
		return m, nil
	case "shift+tab":
		m.inputIndex = (m.inputIndex - 1 + 5) % 5
		m.updateInputFocus()
		return m, nil
	case "enter":
		if m.inputIndex == 4 {
			m.loading = true
			return m, tea.Batch(createDroplet(m.client, m), m.spinner.Tick)
		}
		m.inputIndex = (m.inputIndex + 1) % 5
		m.updateInputFocus()
		return m, nil
	}

	switch m.inputIndex {
	case 0:
		m.nameInput, cmd = m.nameInput.Update(msg)
	case 1:
		m.regionInput, cmd = m.regionInput.Update(msg)
	case 2:
		m.sizeInput, cmd = m.sizeInput.Update(msg)
	case 3:
		m.imageInput, cmd = m.imageInput.Update(msg)
	case 4:
		m.tagsInput, cmd = m.tagsInput.Update(msg)
	}

	return m, cmd
}

func (m *model) updateInputFocus() {
	m.nameInput.Blur()
	m.regionInput.Blur()
	m.sizeInput.Blur()
	m.imageInput.Blur()
	m.tagsInput.Blur()

	switch m.inputIndex {
	case 0:
		m.nameInput.Focus()
	case 1:
		m.regionInput.Focus()
	case 2:
		m.sizeInput.Focus()
	case 3:
		m.imageInput.Focus()
	case 4:
		m.tagsInput.Focus()
	}
}

func (m *model) resetInputs() {
	m.nameInput.SetValue("")
	m.regionInput.SetValue("")
	m.sizeInput.SetValue("")
	m.imageInput.SetValue("")
	m.tagsInput.SetValue("")
	m.inputIndex = 0
}

func (m model) View() string {
	if m.confirmDelete {
		return m.renderDeleteConfirmation()
	}

	if m.creating {
		return m.renderCreateForm()
	}

	if m.viewingDetails && m.selectedDroplet != nil {
		return m.renderDropletDetails()
	}

	return m.renderList()
}

func (m model) renderList() string {
	var s strings.Builder

	// Show loading spinner
	if m.loading {
		s.WriteString(fmt.Sprintf("\n  %s Loading droplets...\n\n", m.spinner.View()))
	}

	s.WriteString(m.list.View())
	s.WriteString("\n")

	// Status bar
	statusBar := statusBarStyle.Render(
		fmt.Sprintf("📊 Droplets: %d  |  Last refresh: %s",
			m.dropletCount,
			m.lastRefresh.Format("15:04:05")),
	)
	s.WriteString(statusBar)
	s.WriteString("\n")

	// Messages
	if m.err != nil {
		s.WriteString(errorMessageStyle.Render(fmt.Sprintf("❌ Error: %v", m.err)))
		s.WriteString("\n")
	}

	if m.successMsg != "" {
		s.WriteString(statusMessageStyle.Render(m.successMsg))
		s.WriteString("\n")
	}

	// Help text
	if m.showHelp {
		helpText := helpStyle.Render(
			"[n] Create  [r] Refresh  [d] Delete  [enter] Details  [q] Quit  [/] Filter",
		)
		s.WriteString(helpText)
		s.WriteString("\n")
	}

	return docStyle.Render(s.String())
}

func (m model) renderDeleteConfirmation() string {
	var s strings.Builder
	s.WriteString("\n")

	warningBox := boxStyle.
		BorderForeground(warningColor).
		Width(60).
		Render(
			fmt.Sprintf(
				"⚠️  Delete Droplet?\n\n"+
					"Droplet: %s (ID: %d)\n\n"+
					"This action cannot be undone!\n\n"+
					"[y] Yes, delete  [n] No, cancel",
				m.deleteTargetName,
				m.deleteTargetID,
			),
		)

	s.WriteString(lipgloss.PlaceHorizontal(80, lipgloss.Center, warningBox))
	s.WriteString("\n")

	return docStyle.Render(s.String())
}

func (m model) renderCreateForm() string {
	var s strings.Builder
	s.WriteString("\n")

	formTitle := titleStyle.Render("✨ Create New Droplet")
	s.WriteString(formTitle)
	s.WriteString("\n\n")

	inputs := []struct {
		label string
		input textinput.Model
		hint  string
	}{
		{"Name:", m.nameInput, "e.g., my-droplet"},
		{"Region:", m.regionInput, "e.g., nyc3, sfo3, ams3"},
		{"Size:", m.sizeInput, "e.g., s-1vcpu-1gb, s-2vcpu-4gb"},
		{"Image:", m.imageInput, "e.g., ubuntu-22-04-x64"},
		{"Tags:", m.tagsInput, "comma-separated, e.g., web,production"},
	}

	for i, item := range inputs {
		labelStyle := lipgloss.NewStyle().Width(20).Foreground(mutedColor)
		if i == m.inputIndex {
			labelStyle = labelStyle.Foreground(primaryColor).Bold(true)
		}

		hintStyle := lipgloss.NewStyle().Foreground(mutedColor).Italic(true)

		s.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render(item.label), item.input.View()))
		if i == m.inputIndex {
			s.WriteString(fmt.Sprintf("   %s\n", hintStyle.Render(item.hint)))
		}
		s.WriteString("\n")
	}

	if m.loading {
		s.WriteString(fmt.Sprintf("  %s Creating droplet...\n\n", m.spinner.View()))
	}

	if m.err != nil {
		s.WriteString(errorMessageStyle.Render(fmt.Sprintf("❌ Error: %v", m.err)))
		s.WriteString("\n")
	}

	helpText := helpStyle.Render("[tab] Next  [shift+tab] Previous  [enter] Create  [esc] Cancel")
	s.WriteString(helpText)
	s.WriteString("\n")

	return docStyle.Render(s.String())
}

func (m model) renderDropletDetails() string {
	if m.selectedDroplet == nil {
		return ""
	}

	d := m.selectedDroplet
	var s strings.Builder

	// Header with status
	statusColor := successColor
	statusIcon := "●"
	if d.Status == "off" {
		statusColor = errorColor
		statusIcon = "○"
	} else if d.Status == "new" {
		statusColor = warningColor
		statusIcon = "◐"
	}

	statusStyle := lipgloss.NewStyle().Foreground(statusColor).Bold(true)
	headerText := fmt.Sprintf("📦 %s  %s", d.Name, statusStyle.Render(fmt.Sprintf("%s %s", statusIcon, strings.ToUpper(d.Status))))

	headerBox := boxStyle.
		Width(70).
		Render(headerText)

	s.WriteString(headerBox)
	s.WriteString("\n\n")

	// Details in two columns
	labelStyle := lipgloss.NewStyle().Foreground(primaryColor).Bold(true).Width(15)
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("255"))

	details := []struct {
		label string
		value string
	}{
		{"🆔 ID:", fmt.Sprintf("%d", d.ID)},
		{"📍 Region:", fmt.Sprintf("%s (%s)", d.Region.Name, d.Region.Slug)},
		{"💾 Size:", fmt.Sprintf("%s - %d vCPU, %dGB RAM", d.SizeSlug, d.Size.Vcpus, d.Size.Memory)},
		{"🖼️  Image:", d.Image.Name},
		{"📅 Created:", d.Created},
	}

	// IP Addresses
	var ipv4s []string
	var ipv6s []string
	for _, v4 := range d.Networks.V4 {
		ipv4s = append(ipv4s, v4.IPAddress)
	}
	for _, v6 := range d.Networks.V6 {
		ipv6s = append(ipv6s, v6.IPAddress)
	}

	if len(ipv4s) > 0 {
		details = append(details, struct {
			label string
			value string
		}{"🌐 IPv4:", strings.Join(ipv4s, ", ")})
	}
	if len(ipv6s) > 0 {
		details = append(details, struct {
			label string
			value string
		}{"🌐 IPv6:", strings.Join(ipv6s, ", ")})
	}

	// Tags
	if len(d.Tags) > 0 {
		tagStyle := lipgloss.NewStyle().
			Foreground(primaryColor).
			Background(bgColor).
			Padding(0, 1).
			Margin(0, 1)
		tagValues := make([]string, len(d.Tags))
		for i, tag := range d.Tags {
			tagValues[i] = tagStyle.Render(tag)
		}
		details = append(details, struct {
			label string
			value string
		}{"🏷️  Tags:", strings.Join(tagValues, " ")})
	}

	// Render details
	detailsBox := boxStyle.Width(70)
	var detailsContent strings.Builder
	for _, detail := range details {
		detailsContent.WriteString(fmt.Sprintf("%s %s\n",
			labelStyle.Render(detail.label),
			valueStyle.Render(detail.value)))
	}

	s.WriteString(detailsBox.Render(detailsContent.String()))
	s.WriteString("\n\n")

	helpText := helpStyle.Render("[esc/enter] Back  [q] Quit")
	s.WriteString(helpText)
	s.WriteString("\n")

	return docStyle.Render(s.String())
}

func loadDroplets(client *godo.Client) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		opt := &godo.ListOptions{PerPage: 200}
		droplets, _, err := client.Droplets.List(ctx, opt)
		if err != nil {
			return errMsg(err)
		}
		return dropletsLoadedMsg(droplets)
	}
}

func createDroplet(client *godo.Client, m model) tea.Cmd {
	return func() tea.Msg {
		name := strings.TrimSpace(m.nameInput.Value())
		region := strings.TrimSpace(m.regionInput.Value())
		size := strings.TrimSpace(m.sizeInput.Value())
		image := strings.TrimSpace(m.imageInput.Value())
		tagsStr := strings.TrimSpace(m.tagsInput.Value())

		if name == "" || region == "" || size == "" || image == "" {
			return errMsg(fmt.Errorf("name, region, size, and image are required"))
		}

		var tags []string
		if tagsStr != "" {
			tagList := strings.Split(tagsStr, ",")
			for _, tag := range tagList {
				tags = append(tags, strings.TrimSpace(tag))
			}
		}

		createRequest := &godo.DropletCreateRequest{
			Name:   name,
			Region: region,
			Size:   size,
			Image: godo.DropletCreateImage{
				Slug: image,
			},
			IPv6: true,
			Tags: tags,
		}

		ctx := context.Background()
		droplet, _, err := client.Droplets.Create(ctx, createRequest)
		if err != nil {
			return errMsg(err)
		}

		time.Sleep(1 * time.Second)
		return dropletCreatedMsg(droplet)
	}
}

func deleteDroplet(client *godo.Client, id int) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		_, err := client.Droplets.Delete(ctx, id)
		if err != nil {
			return errMsg(err)
		}
		time.Sleep(500 * time.Millisecond)
		return dropletDeletedMsg{}
	}
}

func main() {
	token := os.Getenv("DO_TOKEN")
	if token == "" {
		fmt.Fprintf(os.Stderr, "❌ Error: DO_TOKEN environment variable is not set\n")
		fmt.Fprintf(os.Stderr, "Please set it with: export DO_TOKEN=your_token_here\n")
		os.Exit(1)
	}

	tokenSource := &TokenSource{AccessToken: token}
	oauthClient := oauth2.NewClient(context.Background(), tokenSource)
	client := godo.NewClient(oauthClient)

	m := initialModel(client)
	p := tea.NewProgram(m, tea.WithAltScreen())

	if err := p.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
		os.Exit(1)
	}
}
