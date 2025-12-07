package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/digitalocean/godo"
	"golang.org/x/oauth2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type TokenSource struct {
	AccessToken string
}

func (t *TokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{
		AccessToken: t.AccessToken,
	}, nil
}

type model struct {
	table            table.Model
	client           *godo.Client
	droplets         []godo.Droplet
	clusters         []*godo.KubernetesCluster
	clusterResources []map[string]interface{} // Resources from selected cluster
	account          *godo.Account
	creating         bool
	viewingDetails   bool
	confirmDelete    bool
	deleteTargetID   int
	deleteTargetName string
	loading          bool
	spinner          spinner.Model
	selectedDroplet  *godo.Droplet
	selectedCluster  *godo.KubernetesCluster
	currentView      string // "droplets", "clusters", or "cluster-resources"
	clusterResourceType string // "deployments", "pods", "services", "nodes", etc.
	selectedNamespace string // Current namespace filter (empty = all namespaces)
	commandMode      bool   // Command input mode (like k9s :command)
	commandInput     textinput.Model
	nameInput        textinput.Model
	regionInput      textinput.Model
	sizeInput        textinput.Model
	imageInput       textinput.Model
	tagsInput        textinput.Model
	inputIndex       int
	err              error
	successMsg       string
	dropletCount     int
	clusterCount     int
	lastRefresh      time.Time
	selectedRegion   string
	regions          []string
	width            int
	height           int
}

type errMsg error
type dropletsLoadedMsg []godo.Droplet
type clustersLoadedMsg []*godo.KubernetesCluster
type clusterResourcesLoadedMsg struct {
	resourceType string
	resources    []map[string]interface{}
}
type dropletCreatedMsg *godo.Droplet
type dropletDeletedMsg struct{}
type accountInfoMsg struct {
	account *godo.Account
}

var (
	// Colors matching k9s style
	primaryColor   = lipgloss.Color("39")  // cyan
	successColor   = lipgloss.Color("46")  // green
	errorColor     = lipgloss.Color("196") // red
	warningColor   = lipgloss.Color("226") // yellow
	mutedColor     = lipgloss.Color("240") // gray
	bgColor        = lipgloss.Color("235") // dark gray
	borderColor    = lipgloss.Color("39")  // cyan
	highlightColor = lipgloss.Color("226") // yellow for highlights
	
	// SSH connection info (set when user wants to SSH)
	sshIP   string
	sshName string

	// Panel styles
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor).
			Padding(0, 1)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor)

	labelStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	valueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("255"))

	keyStyle = lipgloss.NewStyle().
			Foreground(warningColor).
			Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	errorMessageStyle = lipgloss.NewStyle().
				Foreground(errorColor).
				Bold(true)

	statusMessageStyle = lipgloss.NewStyle().
				Foreground(successColor).
				Bold(true)
)

func initialModel(client *godo.Client) model {
	// Initial columns - will be recalculated on resize
	columns := []table.Column{
		{Title: "NAME", Width: 25},
		{Title: "STATUS", Width: 10},
		{Title: "REGION", Width: 10},
		{Title: "SIZE", Width: 15},
		{Title: "IP", Width: 16},
		{Title: "IMAGE", Width: 20},
		{Title: "AGE", Width: 10},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(15),
	)

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
		BorderBottom(true).
		Bold(true).
		Foreground(primaryColor)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(true)
	t.SetStyles(s)

	// Input widths will be updated on window resize
	nameInput := textinput.New()
	nameInput.Placeholder = "my-droplet"
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

	commandInput := textinput.New()
	commandInput.Placeholder = "deployments, pods, services..."
	commandInput.CharLimit = 50
	commandInput.Width = 50
	commandInput.PromptStyle = lipgloss.NewStyle().Foreground(warningColor)
	commandInput.TextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(primaryColor)

	return model{
		table:           t,
		client:          client,
		droplets:        []godo.Droplet{},
		clusters:        []*godo.KubernetesCluster{},
		clusterResources: []map[string]interface{}{},
		account:         nil,
		creating:        false,
		viewingDetails:  false,
		confirmDelete:   false,
		loading:         false,
		spinner:         sp,
		selectedDroplet: nil,
		selectedCluster: nil,
		currentView:     "droplets", // Start with droplets view
		clusterResourceType: "deployments", // Default resource type when entering cluster
		selectedNamespace: "", // Empty = all namespaces
		commandMode:      false,
		commandInput:     commandInput,
		nameInput:       nameInput,
		regionInput:     regionInput,
		sizeInput:       sizeInput,
		imageInput:      imageInput,
		tagsInput:       tagsInput,
		inputIndex:      0,
		dropletCount:    0,
		clusterCount:    0,
		lastRefresh:     time.Now(),
		selectedRegion:  "all",
		regions:         []string{"all"},
		width:           120,
		height:          40,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		loadDroplets(m.client),
		loadClusters(m.client),
		loadAccountInfo(m.client),
		tea.EnterAltScreen,
		m.spinner.Tick,
		tea.WindowSize(), // Get initial window size
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle command mode first
		if m.commandMode {
			return m.updateCommandMode(msg)
		}

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
		case ":":
			// Enter command mode (only in cluster-resources view)
			if m.currentView == "cluster-resources" {
				m.commandMode = true
				m.commandInput.Focus()
				m.commandInput.SetValue("")
				return m, nil
			}
		case "ctrl+c", "q":
			if m.commandMode {
				m.commandMode = false
				m.commandInput.Blur()
				return m, nil
			}
			return m, tea.Quit
		case "1":
			// Switch to droplets view
			m.currentView = "droplets"
			m.loading = true
			// Update table immediately with droplet columns
			m.updateTableRows()
			return m, tea.Batch(loadDroplets(m.client), m.spinner.Tick)
		case "2":
			// Switch to clusters view
			m.currentView = "clusters"
			m.loading = true
			// Update table immediately with cluster columns
			m.updateTableRows()
			return m, tea.Batch(loadClusters(m.client), m.spinner.Tick)
		case "n", "N":
			if m.currentView == "cluster-resources" {
				// Switch namespace - if viewing namespaces, select one; otherwise toggle all/specific
				if m.clusterResourceType == "namespaces" {
					// If viewing namespaces list, select the highlighted namespace
					if m.table.SelectedRow() != nil && len(m.table.SelectedRow()) > 0 {
						selectedName := m.table.SelectedRow()[0]
						if selectedName == "all" || selectedName == "No data" {
							m.selectedNamespace = ""
						} else {
							m.selectedNamespace = selectedName
						}
						// Reload current resource type with new namespace filter
						m.loading = true
						return m, tea.Batch(loadClusterResources(m.client, m.selectedCluster, m.clusterResourceType, m.selectedNamespace), m.spinner.Tick)
					}
				} else {
					// Toggle between all namespaces and current namespace
					if m.selectedNamespace == "" {
						// Switch to viewing namespaces to select one
						m.clusterResourceType = "namespaces"
						m.loading = true
						return m, tea.Batch(loadClusterResources(m.client, m.selectedCluster, "namespaces", ""), m.spinner.Tick)
					} else {
						// Clear namespace filter (show all)
						m.selectedNamespace = ""
						m.loading = true
						return m, tea.Batch(loadClusterResources(m.client, m.selectedCluster, m.clusterResourceType, ""), m.spinner.Tick)
					}
				}
			} else if m.currentView == "droplets" {
				m.creating = true
				m.inputIndex = 0
				m.nameInput.Focus()
				m.err = nil
				m.successMsg = ""
				return m, nil
			}
		case "r", "R":
			m.loading = true
			if m.currentView == "droplets" {
				return m, tea.Batch(loadDroplets(m.client), m.spinner.Tick)
			} else if m.currentView == "cluster-resources" {
				return m, tea.Batch(loadClusterResources(m.client, m.selectedCluster, m.clusterResourceType, m.selectedNamespace), m.spinner.Tick)
			} else {
				return m, tea.Batch(loadClusters(m.client), m.spinner.Tick)
			}
		case "d", "D":
			// Switch resource types in cluster view, or delete in droplets view
			if m.currentView == "cluster-resources" {
				resourceTypes := []string{"deployments", "pods", "services", "daemonsets", "statefulsets", "pvc", "configmaps", "secrets", "nodes", "namespaces"}
				currentIdx := -1
				for i, rt := range resourceTypes {
					if rt == m.clusterResourceType {
						currentIdx = i
						break
					}
				}
				if currentIdx >= 0 {
					currentIdx = (currentIdx + 1) % len(resourceTypes)
					m.clusterResourceType = resourceTypes[currentIdx]
					m.loading = true
					m.updateTableRows()
					return m, tea.Batch(loadClusterResources(m.client, m.selectedCluster, m.clusterResourceType, m.selectedNamespace), m.spinner.Tick)
				}
			} else if m.currentView == "droplets" {
				if m.table.SelectedRow() != nil && len(m.table.SelectedRow()) > 0 {
					// Find droplet by name
					selectedName := m.table.SelectedRow()[0]
					for _, d := range m.droplets {
						if d.Name == selectedName {
							m.confirmDelete = true
							m.deleteTargetID = d.ID
							m.deleteTargetName = d.Name
							break
						}
					}
				}
			}
			return m, nil
		case "s", "S":
			// SSH into selected droplet
			if m.currentView == "droplets" {
				if m.table.SelectedRow() != nil && len(m.table.SelectedRow()) > 0 {
					selectedName := m.table.SelectedRow()[0]
					for _, d := range m.droplets {
						if d.Name == selectedName {
							// Get IP address
							var ip string
							if len(d.Networks.V4) > 0 {
								ip = d.Networks.V4[0].IPAddress
							}
							
							if ip == "" {
								m.err = fmt.Errorf("droplet %s has no IP address", d.Name)
								return m, nil
							}
							
							// Check if droplet is active
							if d.Status != "active" {
								m.err = fmt.Errorf("droplet %s is not active (status: %s)", d.Name, d.Status)
								return m, nil
							}
							
							// Store SSH info and exit program
							sshIP = ip
							sshName = d.Name
							return m, tea.Sequence(
								tea.ExitAltScreen,
								tea.Quit,
							)
						}
					}
				}
			}
			return m, nil
		case "enter":
			if m.table.SelectedRow() != nil && len(m.table.SelectedRow()) > 0 {
				selectedName := m.table.SelectedRow()[0]
				if m.currentView == "droplets" {
					for i := range m.droplets {
						if m.droplets[i].Name == selectedName {
							m.viewingDetails = true
							m.selectedDroplet = &m.droplets[i]
							m.selectedCluster = nil
							break
						}
					}
				} else if m.currentView == "clusters" {
					// Enter cluster - show resource types menu
					for i := range m.clusters {
						if m.clusters[i].Name == selectedName {
							m.currentView = "cluster-resources"
							m.selectedCluster = m.clusters[i]
							m.selectedDroplet = nil
							m.clusterResourceType = "deployments" // Default to deployments
							m.selectedNamespace = "" // Start with all namespaces
							m.loading = true
							m.updateTableRows()
							return m, tea.Batch(loadClusterResources(m.client, m.clusters[i], "deployments", ""), m.spinner.Tick)
						}
					}
				} else if m.currentView == "cluster-resources" && m.clusterResourceType == "namespaces" {
					// Select namespace when viewing namespaces
					if selectedName == "all" {
						m.selectedNamespace = ""
					} else {
						m.selectedNamespace = selectedName
					}
					// Go back to previous resource type (or deployments if none)
					if m.clusterResourceType == "namespaces" {
						m.clusterResourceType = "deployments"
					}
					m.loading = true
					return m, tea.Batch(loadClusterResources(m.client, m.selectedCluster, m.clusterResourceType, m.selectedNamespace), m.spinner.Tick)
				}
			}
			return m, nil
		case "0":
			if m.currentView == "droplets" {
				m.selectedRegion = "all"
				m.updateTableRows()
			}
			return m, nil
		case "3", "4", "5", "6", "7", "8", "9":
			if m.currentView == "droplets" {
				idx, _ := strconv.Atoi(msg.String())
				if idx > 0 && idx <= len(m.regions) {
					m.selectedRegion = m.regions[idx-1]
					m.updateTableRows()
				}
			}
			return m, nil
		case "esc":
			// Go back from cluster resources to clusters list
			if m.currentView == "cluster-resources" {
				m.currentView = "clusters"
				m.selectedCluster = nil
				m.updateTableRows()
				return m, nil
			}
			return m, nil
		}

	case spinner.TickMsg:
		if m.loading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}

	case dropletsLoadedMsg:
		m.loading = false
		m.droplets = msg
		m.dropletCount = len(msg)
		m.lastRefresh = time.Now()

		// Extract unique regions
		regionMap := make(map[string]bool)
		regionMap["all"] = true
		for _, d := range msg {
			regionMap[d.Region.Slug] = true
		}
		m.regions = []string{"all"}
		for r := range regionMap {
			if r != "all" {
				m.regions = append(m.regions, r)
			}
		}

		// Update table rows first
		m.updateTableRows()
		// Then update all dimensions to ensure proper sizing
		m.updateAllDimensions(m.width, m.height)
		return m, tea.Batch(cmds...)

	case clustersLoadedMsg:
		m.loading = false
		m.clusters = msg
		m.clusterCount = len(msg)
		m.lastRefresh = time.Now()

		// Update table rows for clusters
		m.updateTableRows()
		// Then update all dimensions to ensure proper sizing
		m.updateAllDimensions(m.width, m.height)
		return m, tea.Batch(cmds...)

	case clusterResourcesLoadedMsg:
		m.loading = false
		m.clusterResources = msg.resources
		m.clusterResourceType = msg.resourceType
		m.lastRefresh = time.Now()

		// Update table rows for cluster resources
		m.updateTableRows()
		// Then update all dimensions to ensure proper sizing
		m.updateAllDimensions(m.width, m.height)
		return m, tea.Batch(cmds...)

	case accountInfoMsg:
		m.account = msg.account
		return m, nil

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
		// iTerm optimization: Store raw dimensions accurately
		rawWidth := msg.Width
		rawHeight := msg.Height

		// iTerm: Ensure we use actual terminal dimensions
		// Don't override if terminal reports valid size
		if rawWidth <= 0 {
			rawWidth = 120 // Fallback for iTerm if detection fails
		}
		if rawHeight <= 0 {
			rawHeight = 40 // Fallback for iTerm if detection fails
		}

		// Ensure minimum dimensions but store actual values
		if rawWidth < 40 {
			rawWidth = 40
		}
		if rawHeight < 10 {
			rawHeight = 10
		}

		// Update model dimensions - iTerm will use actual reported size
		m.width = rawWidth
		m.height = rawHeight

		// Immediately update all dynamic components
		m.updateAllDimensions(rawWidth, rawHeight)

		// Return nil to trigger a re-render
		return m, nil
	}

	if !m.loading && !m.creating && !m.viewingDetails && !m.confirmDelete {
		m.table, cmd = m.table.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// updateAllDimensions updates all UI components based on current window size
func (m *model) updateAllDimensions(width, height int) {
	// Update table dimensions
	m.updateTableDimensions(width, height)

	// Update input widths for create form
	m.updateInputWidths(width)
}

func (m *model) updateTableDimensions(width, height int) {
	// Ensure minimum dimensions
	if width < 40 {
		width = 40
	}
	if height < 10 {
		height = 10
	}

	// Calculate top bar height based on layout
	topBarHeight := m.getTopBarHeight(width)

	// Calculate available width for table - use full width minus small margin
	tableWidth := width - 2
	if tableWidth < 50 {
		tableWidth = 50
	}

	// Calculate available height: total height - top bar - status bar - padding
	// Status bar: 1 line, padding: 1 line
	tableHeight := height - topBarHeight - 2
	if tableHeight < 3 {
		tableHeight = 3
	}
	if tableHeight > 100 {
		tableHeight = 100 // Reasonable maximum
	}

	// Set table dimensions - this must happen before column width calculation
	m.table.SetWidth(tableWidth)
	m.table.SetHeight(tableHeight)

	// Update column widths based on available space
	m.updateColumnWidths(tableWidth)
}

func (m *model) updateInputWidths(width int) {
	// Calculate optimal input width based on terminal width
	inputWidth := width - 30
	if inputWidth > 60 {
		inputWidth = 60
	}
	if inputWidth < 30 {
		inputWidth = 30
	}

	// Update all input widths
	m.nameInput.Width = inputWidth
	m.regionInput.Width = inputWidth
	m.sizeInput.Width = inputWidth
	m.imageInput.Width = inputWidth
	m.tagsInput.Width = inputWidth
}

func (m *model) getTopBarHeight(width int) int {
	// k9s-style top bar heights
	if width >= 120 {
		return 7 // 3-panel layout with multiple lines
	} else if width >= 80 {
		return 5 // 2-panel layout
	} else {
		return 2 // Compact single/two line
	}
}

func (m *model) updateColumnWidths(totalWidth int) {
	// Skip column width calculation if we're in clusters view - columns are set in updateTableRows
	if m.currentView == "clusters" {
		return
	}
	
	// Minimum column widths (optimized for small screens)
	// STATUS needs more width to show full status text like "ACTIVE"
	minWidths := map[string]int{
		"NAME":   8,
		"STATUS": 10, // Increased to show full status like "● ACTIVE"
		"REGION": 5,
		"SIZE":   7,
		"IP":     9,
		"IMAGE":  8,
		"AGE":    3,
	}

	// Proportional widths (percentages) - must sum to ~1.0
	proportions := map[string]float64{
		"NAME":   0.28,
		"STATUS": 0.10,
		"REGION": 0.09,
		"SIZE":   0.13,
		"IP":     0.13,
		"IMAGE":  0.20,
		"AGE":    0.07,
	}

	// Account for table borders and spacing (approximately 4-6 chars)
	// The table component adds some padding internally
	availableWidth := totalWidth - 6
	if availableWidth < 45 {
		availableWidth = 45
	}

	// Calculate initial column widths
	columns := []table.Column{
		{Title: "NAME", Width: max(int(float64(availableWidth)*proportions["NAME"]), minWidths["NAME"])},
		{Title: "STATUS", Width: max(int(float64(availableWidth)*proportions["STATUS"]), minWidths["STATUS"])},
		{Title: "REGION", Width: max(int(float64(availableWidth)*proportions["REGION"]), minWidths["REGION"])},
		{Title: "SIZE", Width: max(int(float64(availableWidth)*proportions["SIZE"]), minWidths["SIZE"])},
		{Title: "IP", Width: max(int(float64(availableWidth)*proportions["IP"]), minWidths["IP"])},
		{Title: "IMAGE", Width: max(int(float64(availableWidth)*proportions["IMAGE"]), minWidths["IMAGE"])},
		{Title: "AGE", Width: max(int(float64(availableWidth)*proportions["AGE"]), minWidths["AGE"])},
	}

	// Calculate total width
	total := 0
	for _, col := range columns {
		total += col.Width
	}

	// If total exceeds available width, scale down proportionally
	if total > availableWidth {
		scale := float64(availableWidth) / float64(total)
		for i := range columns {
			newWidth := int(float64(columns[i].Width) * scale)
			columns[i].Width = max(newWidth, minWidths[columns[i].Title])
		}

		// Recalculate and adjust if still too wide
		total = 0
		for _, col := range columns {
			total += col.Width
		}

		if total > availableWidth {
			// Reduce from least important columns first (IMAGE, then SIZE)
			excess := total - availableWidth
			for excess > 0 {
				reduced := false
				if columns[6].Width > minWidths["IMAGE"] {
					reduce := min(excess, columns[6].Width-minWidths["IMAGE"])
					columns[6].Width -= reduce
					excess -= reduce
					reduced = true
				}
				if excess > 0 && columns[3].Width > minWidths["SIZE"] {
					reduce := min(excess, columns[3].Width-minWidths["SIZE"])
					columns[3].Width -= reduce
					excess -= reduce
					reduced = true
				}
				if !reduced {
					break // Can't reduce further
				}
			}
		}
	}

	// Apply the columns
	m.table.SetColumns(columns)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (m *model) updateTableRows() {
	var rows []table.Row
	
	if m.currentView == "cluster-resources" {
		// Show cluster resources (deployments, pods, etc.)
		m.updateClusterResourceTable()
		return
	} else if m.currentView == "clusters" {
		// Update table columns for clusters
		m.table.SetColumns([]table.Column{
			{Title: "NAME", Width: 30},
			{Title: "STATUS", Width: 12},
			{Title: "REGION", Width: 10},
			{Title: "VERSION", Width: 12},
			{Title: "NODE POOLS", Width: 12},
			{Title: "NODES", Width: 10},
			{Title: "AGE", Width: 10},
		})
		
		// Add cluster rows
		for _, c := range m.clusters {
			status := string(c.Status.State)
			statusColor := successColor
			statusIcon := "●"
			if status == "degraded" || status == "error" {
				statusColor = errorColor
				statusIcon = "○"
			} else if status == "provisioning" || status == "running_setup" {
				statusColor = warningColor
				statusIcon = "◐"
			}
			statusStyle := lipgloss.NewStyle().Foreground(statusColor).Bold(true)
			statusDisplay := statusStyle.Render(fmt.Sprintf("%s %s", statusIcon, strings.ToUpper(status)))
			
			// Count node pools and nodes
			nodePoolCount := len(c.NodePools)
			totalNodes := 0
			for _, np := range c.NodePools {
				totalNodes += np.Count
			}
			
			// Format age
			age := "N/A"
			if !c.CreatedAt.IsZero() {
				duration := time.Since(c.CreatedAt)
				if duration.Hours() < 24 {
					age = fmt.Sprintf("%.0fh", duration.Hours())
				} else {
					age = fmt.Sprintf("%.0fd", duration.Hours()/24)
				}
			}
			
			rows = append(rows, table.Row{
				c.Name,
				statusDisplay,
				c.RegionSlug,
				c.VersionSlug,
				fmt.Sprintf("%d", nodePoolCount),
				fmt.Sprintf("%d", totalNodes),
				age,
			})
		}
	} else {
		// Update table columns for droplets
		m.table.SetColumns([]table.Column{
			{Title: "NAME", Width: 25},
			{Title: "STATUS", Width: 10},
			{Title: "REGION", Width: 10},
			{Title: "SIZE", Width: 15},
			{Title: "IP", Width: 16},
			{Title: "IMAGE", Width: 20},
			{Title: "AGE", Width: 10},
		})
		
		// Add droplet rows
		for _, d := range m.droplets {
			if m.selectedRegion != "all" && d.Region.Slug != m.selectedRegion {
				continue
			}

			status := d.Status
			statusColor := successColor
			statusIcon := "●"
			if status == "off" {
				statusColor = errorColor
				statusIcon = "○"
			} else if status == "new" {
				statusColor = warningColor
				statusIcon = "◐"
			}
			statusStyle := lipgloss.NewStyle().Foreground(statusColor).Bold(true)
			statusText := strings.ToUpper(status)
			statusDisplay := statusStyle.Render(fmt.Sprintf("%s %s", statusIcon, statusText))

			ip := "No IP"
			if len(d.Networks.V4) > 0 {
				ip = d.Networks.V4[0].IPAddress
			}

			// Format size
			sizeDisplay := d.SizeSlug
			if strings.Contains(d.SizeSlug, "-") {
				parts := strings.Split(d.SizeSlug, "-")
				if len(parts) >= 3 {
					sizeDisplay = fmt.Sprintf("%s %s", parts[1], parts[2])
				}
			}

			// Format age
			age := "N/A"
			if d.Created != "" {
				if t, err := time.Parse(time.RFC3339, d.Created); err == nil {
					duration := time.Since(t)
					if duration.Hours() < 24 {
						age = fmt.Sprintf("%.0fh", duration.Hours())
					} else {
						age = fmt.Sprintf("%.0fd", duration.Hours()/24)
					}
				}
			}

			rows = append(rows, table.Row{
				d.Name,
				statusDisplay,
				d.Region.Slug,
				sizeDisplay,
				ip,
				d.Image.Name,
				age,
			})
		}
	}

	m.table.SetRows(rows)
}

// Helper function to safely get map value with default
func getMapValue(r map[string]interface{}, key string, defaultValue string) string {
	if val, ok := r[key]; ok && val != nil {
		return fmt.Sprintf("%v", val)
	}
	return defaultValue
}

func (m *model) updateClusterResourceTable() {
	// Update table columns based on resource type
	var columns []table.Column
	var rows []table.Row
	
	switch m.clusterResourceType {
	case "deployments":
		columns = []table.Column{
			{Title: "NAME", Width: 30},
			{Title: "READY", Width: 10},
			{Title: "UP-TO-DATE", Width: 12},
			{Title: "AVAILABLE", Width: 12},
			{Title: "AGE", Width: 10},
		}
		// Use actual resources from cluster
		for _, r := range m.clusterResources {
			rows = append(rows, table.Row{
				getMapValue(r, "name", "N/A"),
				getMapValue(r, "ready", "0/0"),
				getMapValue(r, "upToDate", "0"),
				getMapValue(r, "available", "0"),
				getMapValue(r, "age", "N/A"),
			})
		}
	case "pods":
		columns = []table.Column{
			{Title: "NAME", Width: 35},
			{Title: "READY", Width: 10},
			{Title: "STATUS", Width: 12},
			{Title: "RESTARTS", Width: 10},
			{Title: "AGE", Width: 10},
		}
		for _, r := range m.clusterResources {
			rows = append(rows, table.Row{
				getMapValue(r, "name", "N/A"),
				getMapValue(r, "ready", "0/0"),
				getMapValue(r, "status", "Unknown"),
				getMapValue(r, "restarts", "0"),
				getMapValue(r, "age", "N/A"),
			})
		}
	case "services":
		columns = []table.Column{
			{Title: "NAME", Width: 30},
			{Title: "TYPE", Width: 12},
			{Title: "CLUSTER-IP", Width: 15},
			{Title: "EXTERNAL-IP", Width: 15},
			{Title: "AGE", Width: 10},
		}
		for _, r := range m.clusterResources {
			rows = append(rows, table.Row{
				getMapValue(r, "name", "N/A"),
				getMapValue(r, "type", "ClusterIP"),
				getMapValue(r, "clusterIP", "<none>"),
				getMapValue(r, "externalIP", "<none>"),
				getMapValue(r, "age", "N/A"),
			})
		}
	case "nodes":
		columns = []table.Column{
			{Title: "NAME", Width: 30},
			{Title: "STATUS", Width: 12},
			{Title: "ROLES", Width: 10},
			{Title: "AGE", Width: 10},
			{Title: "VERSION", Width: 12},
		}
		for _, r := range m.clusterResources {
			rows = append(rows, table.Row{
				getMapValue(r, "name", "N/A"),
				getMapValue(r, "status", "Unknown"),
				getMapValue(r, "roles", "<none>"),
				getMapValue(r, "age", "N/A"),
				getMapValue(r, "version", "N/A"),
			})
		}
	case "namespaces":
		columns = []table.Column{
			{Title: "NAME", Width: 30},
			{Title: "STATUS", Width: 12},
			{Title: "AGE", Width: 10},
		}
		// Add "all" option at the top for selecting all namespaces
		rows = append(rows, table.Row{"all", "Active", "N/A"})
		for _, r := range m.clusterResources {
			rows = append(rows, table.Row{
				getMapValue(r, "name", "N/A"),
				getMapValue(r, "status", "Unknown"),
				getMapValue(r, "age", "N/A"),
			})
		}
	default:
		columns = []table.Column{
			{Title: "NAME", Width: 30},
			{Title: "STATUS", Width: 12},
			{Title: "AGE", Width: 10},
		}
		rows = []table.Row{
			{"No resources", "N/A", "N/A"},
		}
	}
	
	// CRITICAL: Clear rows FIRST before setting columns
	// This prevents the table library from trying to render old rows with new column structure
	m.table.SetRows([]table.Row{})
	
	// Set columns
	m.table.SetColumns(columns)
	
	// Ensure we have at least one row to prevent rendering issues
	if len(rows) == 0 {
		// Create a placeholder row matching the column count
		placeholderRow := make(table.Row, len(columns))
		for i := range placeholderRow {
			placeholderRow[i] = "No data"
		}
		rows = []table.Row{placeholderRow}
	}
	
	// Now set the rows with the correct column structure
	m.table.SetRows(rows)
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

func (m model) updateCommandMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg.String() {
	case "esc":
		m.commandMode = false
		m.commandInput.Blur()
		m.commandInput.SetValue("")
		return m, nil
	case "enter":
		// Execute command
		command := strings.TrimSpace(m.commandInput.Value())
		m.commandMode = false
		m.commandInput.Blur()
		m.commandInput.SetValue("")
		
		if command == "" {
			return m, nil
		}
		
		// Handle resource type switching
		validResources := map[string]bool{
			"deployments": true,
			"pods":        true,
			"services":    true,
			"daemonsets":  true,
			"statefulsets": true,
			"pvc":         true,
			"configmaps":  true,
			"secrets":     true,
			"nodes":       true,
			"namespaces":  true,
		}
		
		// Convert to lowercase for case-insensitive matching
		commandLower := strings.ToLower(command)
		
		if validResources[commandLower] {
			m.clusterResourceType = commandLower
			m.loading = true
			m.updateTableRows()
			return m, tea.Batch(loadClusterResources(m.client, m.selectedCluster, m.clusterResourceType, m.selectedNamespace), m.spinner.Tick)
		}
		
		// If command not recognized, show error (could be enhanced)
		return m, nil
	}

	m.commandInput, cmd = m.commandInput.Update(msg)
	return m, cmd
}

func (m model) renderCommandMode() string {
	var s strings.Builder
	
	// Show main view in background
	mainView := m.renderMainView()
	s.WriteString(mainView)
	
	// Overlay command input at bottom
	commandText := m.commandInput.View()
	commandPrompt := lipgloss.NewStyle().
		Foreground(warningColor).
		Bold(true).
		Render(":")
	
	commandLine := lipgloss.NewStyle().
		Background(bgColor).
		Foreground(lipgloss.Color("255")).
		Padding(0, 1).
		Width(m.width).
		Render(commandPrompt + " " + commandText)
	
	s.WriteString("\n")
	s.WriteString(commandLine)
	
	// Show available resources
	availableResources := []string{"deployments", "pods", "services", "daemonsets", "statefulsets", "pvc", "configmaps", "secrets", "nodes", "namespaces"}
	helpText := lipgloss.NewStyle().
		Foreground(mutedColor).
		Render(fmt.Sprintf("Available: %s", strings.Join(availableResources, ", ")))
	
	s.WriteString("\n")
	s.WriteString(helpText)
	
	return s.String()
}

func (m model) View() string {
	if m.commandMode {
		return m.renderCommandMode()
	}

	if m.confirmDelete {
		return m.renderDeleteConfirmation()
	}

	if m.creating {
		return m.renderCreateForm()
	}

	if m.viewingDetails {
		if m.selectedDroplet != nil {
			return m.renderDropletDetails()
		}
		if m.selectedCluster != nil && m.currentView != "cluster-resources" {
			return m.renderClusterDetails()
		}
	}

	return m.renderMainView()
}

func (m model) renderMainView() string {
	var s strings.Builder

	// Ensure we have valid dimensions (defensive check)
	width := m.width
	height := m.height
	if width < 40 {
		width = 40
	}
	if height < 10 {
		height = 10
	}

	// Always recalculate all dimensions before rendering for real-time responsiveness
	m.updateAllDimensions(width, height)

	// Top bar with adaptive panels - dynamically adjusts based on width
	// Force width to be valid before rendering
	if m.width <= 0 {
		m.width = width // Use the validated width
	}
	if m.height <= 0 {
		m.height = height // Use the validated height
	}

	topBar := m.renderTopBar()
	// Ensure top bar always shows something - double check
	if topBar == "" || strings.TrimSpace(topBar) == "" {
		// Fallback if top bar is empty - show minimal summary
		topBar = headerStyle.Render("DigitalOcean") + " | " +
			labelStyle.Render("Droplets: ") + valueStyle.Render(fmt.Sprintf("%d", m.dropletCount)) + " | " +
			labelStyle.Render("Region: ") + valueStyle.Render(m.selectedRegion)
	}

	// Always write the top bar - it should never be empty
	s.WriteString(topBar)
	s.WriteString("\n")

	// Main table area - automatically sized based on current dimensions
	tableView := m.table.View()
	s.WriteString(tableView)
	s.WriteString("\n")

	// Status bar - adapts to current width
	statusBar := m.renderStatusBar()
	s.WriteString(statusBar)

	// Messages
	if m.err != nil {
		s.WriteString("\n")
		s.WriteString(errorMessageStyle.Render(fmt.Sprintf("❌ Error: %v", m.err)))
	}

	if m.successMsg != "" {
		s.WriteString("\n")
		s.WriteString(statusMessageStyle.Render(m.successMsg))
	}

	return s.String()
}

func (m model) renderTopBar() string {
	// k9s-style top bar - always use 3-panel layout if width allows
	width := m.width
	if width <= 0 {
		width = 120
	}
	if width < 40 {
		width = 40
	}

	// For k9s-style, we want 3 panels if width >= 120, otherwise 2 panels, otherwise compact
	if width >= 120 {
		return m.renderTopBarK9sStyle()
	} else if width >= 80 {
		return m.renderTopBarTwoPanelK9s()
	} else {
		return m.renderTopBarCompactK9s()
	}
}

func (m model) renderTopBarUltraCompact() string {
	// Ultra-minimal for very small terminals (< 50 chars)
	// ALWAYS show at least the essential info
	var s strings.Builder
	s.WriteString(headerStyle.Render("DO"))
	s.WriteString(" ")
	s.WriteString(fmt.Sprintf("D:%d", m.dropletCount))
	s.WriteString(" ")
	s.WriteString(fmt.Sprintf("R:%s", truncateString(m.selectedRegion, 8)))
	s.WriteString(" ")
	s.WriteString(keyStyle.Render("n") + keyStyle.Render("r") + keyStyle.Render("d") + keyStyle.Render("q"))
	result := s.String()
	// Ensure we return something
	if result == "" {
		result = fmt.Sprintf("DO D:%d R:%s", m.dropletCount, truncateString(m.selectedRegion, 8))
	}
	return result
}

func (m model) renderTopBarCompact() string {
	// Compact single line (50-70 chars)
	// ALWAYS show essential info
	var s strings.Builder
	s.WriteString(headerStyle.Render("DO"))
	s.WriteString(" ")
	s.WriteString(labelStyle.Render("D:") + valueStyle.Render(fmt.Sprintf("%d", m.dropletCount)))
	s.WriteString(" ")
	s.WriteString(labelStyle.Render("R:") + valueStyle.Render(truncateString(m.selectedRegion, 10)))

	refreshTime := "N/A"
	if !m.lastRefresh.IsZero() {
		refreshTime = m.lastRefresh.Format("15:04")
	}
	s.WriteString(" ")
	s.WriteString(labelStyle.Render("T:") + valueStyle.Render(refreshTime))
	s.WriteString(" | ")
	s.WriteString(keyStyle.Render("n") + " " + keyStyle.Render("r") + " " + keyStyle.Render("d") + " " + keyStyle.Render("q"))
	result := s.String()
	// Ensure we return something
	if result == "" {
		result = fmt.Sprintf("DO D:%d R:%s T:%s", m.dropletCount, truncateString(m.selectedRegion, 10), refreshTime)
	}
	return result
}

func (m model) renderTopBarTwoPanel() string {
	// Two panel layout for medium screens (95-145 chars)
	panelPadding := 4
	availableWidth := m.width - panelPadding
	// Ensure left panel has minimum width for full visibility (minimum 45 chars)
	leftPanelWidth := (availableWidth / 2) - 2
	if leftPanelWidth < 45 {
		leftPanelWidth = 45
	}
	rightPanelWidth := availableWidth - leftPanelWidth - 4
	if rightPanelWidth < 35 {
		rightPanelWidth = 35
		leftPanelWidth = availableWidth - rightPanelWidth - 4
		if leftPanelWidth < 40 {
			return m.renderTopBarVertical()
		}
	}
	panelWidth := leftPanelWidth

	// Left panel - Account info (Summary)
	var accountInfo strings.Builder
	accountInfo.WriteString(headerStyle.Render("DigitalOcean"))
	accountInfo.WriteString("\n")

	// Always show essential summary info - prioritize visibility
	if m.currentView == "cluster-resources" && m.selectedCluster != nil {
		accountInfo.WriteString(labelStyle.Render("Cluster: ") + valueStyle.Render(m.selectedCluster.Name))
		accountInfo.WriteString("\n")
		accountInfo.WriteString(labelStyle.Render("Region: ") + valueStyle.Render(m.selectedCluster.RegionSlug))
		accountInfo.WriteString("\n")
		namespaceDisplay := "all"
		if m.selectedNamespace != "" {
			namespaceDisplay = m.selectedNamespace
		}
		accountInfo.WriteString(labelStyle.Render("Namespace: ") + valueStyle.Render(namespaceDisplay))
		accountInfo.WriteString("\n")
		accountInfo.WriteString(labelStyle.Render("Resource: ") + valueStyle.Render(strings.Title(m.clusterResourceType)))
		accountInfo.WriteString("\n")
		accountInfo.WriteString(labelStyle.Render("Count: ") + valueStyle.Render(fmt.Sprintf("%d", len(m.clusterResources))))
	} else if m.currentView == "clusters" {
		accountInfo.WriteString(labelStyle.Render("View: ") + valueStyle.Render("Kubernetes Clusters"))
		accountInfo.WriteString("\n")
		accountInfo.WriteString(labelStyle.Render("Clusters: ") + valueStyle.Render(fmt.Sprintf("%d", m.clusterCount)))
	} else {
		accountInfo.WriteString(labelStyle.Render("Droplets: ") + valueStyle.Render(fmt.Sprintf("%d", m.dropletCount)))
		accountInfo.WriteString("\n")
		// Don't truncate region - show full region name
		accountInfo.WriteString(labelStyle.Render("Region: ") + valueStyle.Render(m.selectedRegion))
		accountInfo.WriteString("\n")
	}

	refreshTime := "N/A"
	if !m.lastRefresh.IsZero() {
		refreshTime = m.lastRefresh.Format("15:04:05")
	}
	accountInfo.WriteString(labelStyle.Render("Refresh: ") + valueStyle.Render(refreshTime))

	// Show account status if available
	if m.account != nil && panelWidth > 40 {
		accountInfo.WriteString("\n")
		accountInfo.WriteString(labelStyle.Render("Status: ") + valueStyle.Render(m.account.Status))
	}

	// Ensure panel has minimum content even if narrow - make sure it's visible
	leftPanelContent := accountInfo.String()
	// CRITICAL: Ensure content is not empty
	if strings.TrimSpace(leftPanelContent) == "" {
		leftPanelContent = fmt.Sprintf("DigitalOcean\n\nDroplets: %d\nRegion: %s\nRefresh: N/A",
			m.dropletCount, m.selectedRegion)
	}
	leftPanel := panelStyle.
		Width(panelWidth).
		Render(leftPanelContent)

	// Double-check panel is not empty
	if strings.TrimSpace(leftPanel) == "" {
		leftPanel = panelStyle.Width(panelWidth).Render(fmt.Sprintf("DigitalOcean\n\nDroplets: %d\nRegion: %s",
			m.dropletCount, m.selectedRegion))
	}

	// Right panel - Keybindings and Regions
	// CRITICAL: Always show 1, 2, n first - direct write to ensure visibility
	var rightContent strings.Builder
	rightContent.WriteString(headerStyle.Render("Keys"))
	rightContent.WriteString("\n")
	// Direct write to ensure 1, 2, n are always visible
	rightContent.WriteString(keyStyle.Render("1") + " Droplets | ")
	rightContent.WriteString(keyStyle.Render("2") + " Clusters | ")
	rightContent.WriteString(keyStyle.Render("n") + " New | ")
	rightContent.WriteString(keyStyle.Render("r") + " Refresh | ")
	rightContent.WriteString(keyStyle.Render("d") + " Delete | ")
	rightContent.WriteString(keyStyle.Render("s") + " SSH | ")
	rightContent.WriteString(keyStyle.Render("enter") + " View | ")
	rightContent.WriteString(keyStyle.Render("q") + " Quit")
	rightContent.WriteString("\n")
	rightContent.WriteString(headerStyle.Render("Regions"))
	rightContent.WriteString("\n")

	maxRegions := min(6, len(m.regions))
	regionLine := ""
	for i := 0; i < maxRegions; i++ {
		if i > 0 {
			regionLine += " "
		}
		regionLine += fmt.Sprintf("%s %s", keyStyle.Render(fmt.Sprintf("<%d>", i)), truncateString(m.regions[i], 8))
	}
	rightContent.WriteString(regionLine)

	// Use rightPanelWidth if defined, otherwise panelWidth
	rightWidth := rightPanelWidth
	if rightWidth == 0 {
		rightWidth = panelWidth
	}
	rightPanel := panelStyle.Width(rightWidth).Render(rightContent.String())

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)
}

// k9s-style top bar with 3 panels (like k9s)
// Optimized for iTerm and macOS Terminal compatibility
func (m model) renderTopBarK9sStyle() string {
	// Three panel layout matching k9s style
	// iTerm optimization: Account for actual terminal width accurately
	panelPadding := 4
	availableWidth := m.width - panelPadding
	// Ensure we have valid width for iTerm
	if availableWidth < 100 {
		availableWidth = 100
	}
	
	// Left panel: Account/Context info (like k9s Context/Cluster/User)
	// Give left panel more space to ensure all info is visible (minimum 50 chars)
	leftWidth := availableWidth / 3
	if leftWidth < 50 {
		leftWidth = 50
	}
	// iTerm-optimized: Middle panel for keybindings (minimum 25 chars for simple format)
	middleWidth := availableWidth / 3
	if middleWidth < 25 {
		middleWidth = 25
	}
	// Right panel: Logo/Regions (remaining space)
	rightWidth := availableWidth - leftWidth - middleWidth
	if rightWidth < 30 {
		rightWidth = 30
		// Recalculate if we hit minimums - prioritize middle panel for keybindings
		if leftWidth + middleWidth + rightWidth > availableWidth {
			// Reduce left panel if needed, but keep middle panel at minimum
			leftWidth = availableWidth - middleWidth - rightWidth
			if leftWidth < 40 {
				leftWidth = 40
				// If still too wide, reduce right panel
				if leftWidth + middleWidth + rightWidth > availableWidth {
					rightWidth = availableWidth - leftWidth - middleWidth
					if rightWidth < 20 {
						rightWidth = 20
					}
				}
			}
		}
	}

	// Left panel - Context/Account info (k9s style)
	var leftContent strings.Builder
	leftContent.WriteString(labelStyle.Render("Context: ") + valueStyle.Render("DigitalOcean"))
	leftContent.WriteString("\n")
	
	if m.currentView == "cluster-resources" {
		// Show cluster info when viewing cluster resources
		if m.selectedCluster != nil {
			leftContent.WriteString(labelStyle.Render("Cluster: ") + valueStyle.Render(m.selectedCluster.Name))
			leftContent.WriteString("\n")
			leftContent.WriteString(labelStyle.Render("Region: ") + valueStyle.Render(m.selectedCluster.RegionSlug))
			leftContent.WriteString("\n")
			leftContent.WriteString(labelStyle.Render("Version: ") + valueStyle.Render(m.selectedCluster.VersionSlug))
			leftContent.WriteString("\n")
			
			// Show namespace filter
			namespaceDisplay := "all"
			if m.selectedNamespace != "" {
				namespaceDisplay = m.selectedNamespace
			}
			leftContent.WriteString(labelStyle.Render("Namespace: ") + valueStyle.Render(namespaceDisplay))
			leftContent.WriteString("\n")
			
			// Show resource type
			leftContent.WriteString(labelStyle.Render("Resource: ") + valueStyle.Render(strings.Title(m.clusterResourceType)))
			leftContent.WriteString("\n")
			
			// Show resource count
			leftContent.WriteString(labelStyle.Render("Count: ") + valueStyle.Render(fmt.Sprintf("%d", len(m.clusterResources))))
			leftContent.WriteString("\n")
		}
	} else if m.currentView == "clusters" {
		leftContent.WriteString(labelStyle.Render("View: ") + valueStyle.Render("Kubernetes Clusters"))
		leftContent.WriteString("\n")
		leftContent.WriteString(labelStyle.Render("Clusters: ") + valueStyle.Render(fmt.Sprintf("%d", m.clusterCount)))
	} else {
		leftContent.WriteString(labelStyle.Render("Region: ") + valueStyle.Render(m.selectedRegion))
		leftContent.WriteString("\n")
		leftContent.WriteString(labelStyle.Render("Droplets: ") + valueStyle.Render(fmt.Sprintf("%d", m.dropletCount)))
	}
	leftContent.WriteString("\n")
	
	if m.account != nil && m.currentView != "cluster-resources" {
		// Don't truncate email - show full email or use shorter label
		email := m.account.Email
		if len(email) > leftWidth-10 {
			// If email is too long, show first part
			email = email[:min(len(email), leftWidth-10)] + "..."
		}
		leftContent.WriteString(labelStyle.Render("Account: ") + valueStyle.Render(email))
		leftContent.WriteString("\n")
		leftContent.WriteString(labelStyle.Render("Status: ") + valueStyle.Render(m.account.Status))
		leftContent.WriteString("\n")
	}
	
	refreshTime := "N/A"
	if !m.lastRefresh.IsZero() {
		refreshTime = m.lastRefresh.Format("15:04:05")
	}
	leftContent.WriteString(labelStyle.Render("Refresh: ") + valueStyle.Render(refreshTime))
	leftContent.WriteString("\n")
		leftContent.WriteString(labelStyle.Render("Version: ") + valueStyle.Render("dogoctl v1.1.0"))

	// iTerm-optimized: Better padding for left panel
	leftPanel := lipgloss.NewStyle().
		Width(leftWidth).
		Padding(0, 2). // More padding for iTerm
		Render(leftContent.String())

	// Middle panel - Keybindings (k9s style, optimized for iTerm)
	// ALWAYS show 1, 2, n first - prioritize visibility
	var middleContent strings.Builder
	middleContent.WriteString(headerStyle.Render("Keys"))
	middleContent.WriteString("\n")
	
	// iTerm-optimized: Simple format matching the desired output
	// CRITICAL: Always show 1, 2, n first - simple format like "1 Droplets"
	if m.currentView == "cluster-resources" {
		middleContent.WriteString(keyStyle.Render("1") + " Droplets\n")
		middleContent.WriteString(keyStyle.Render("2") + " Clusters\n")
		middleContent.WriteString(keyStyle.Render(":") + " Command\n")
		middleContent.WriteString(keyStyle.Render("d") + " Next\n")
		middleContent.WriteString(keyStyle.Render("n") + " Namespace\n")
		middleContent.WriteString(keyStyle.Render("r") + " Refresh\n")
		middleContent.WriteString(keyStyle.Render("enter") + " Details\n")
		middleContent.WriteString(keyStyle.Render("esc") + " Back\n")
		middleContent.WriteString(keyStyle.Render("q") + " Quit")
	} else if m.currentView == "droplets" {
		// Droplets view - show 1, 2, n prominently (matching image format)
		middleContent.WriteString(keyStyle.Render("1") + " Droplets\n")
		middleContent.WriteString(keyStyle.Render("2") + " Clusters\n")
		middleContent.WriteString(keyStyle.Render("n") + " New\n")
		middleContent.WriteString(keyStyle.Render("r") + " Refresh\n")
		middleContent.WriteString(keyStyle.Render("d") + " Delete\n")
		middleContent.WriteString(keyStyle.Render("s") + " SSH\n")
		middleContent.WriteString(keyStyle.Render("enter") + " Details\n")
		middleContent.WriteString(keyStyle.Render("?") + " Help\n")
		middleContent.WriteString(keyStyle.Render("q") + " Quit")
	} else {
		// Clusters view
		middleContent.WriteString(keyStyle.Render("1") + " Droplets\n")
		middleContent.WriteString(keyStyle.Render("2") + " Clusters\n")
		middleContent.WriteString(keyStyle.Render("r") + " Refresh\n")
		middleContent.WriteString(keyStyle.Render("enter") + " Enter\n")
		middleContent.WriteString(keyStyle.Render("?") + " Help\n")
		middleContent.WriteString(keyStyle.Render("q") + " Quit")
	}

	// iTerm-friendly padding and rendering
	middlePanel := lipgloss.NewStyle().
		Width(middleWidth).
		Padding(0, 1). // Standard padding for iTerm
		Render(middleContent.String())

	// Right panel - Logo and Regions (k9s style)
	var rightContent strings.Builder
	rightContent.WriteString(headerStyle.Render("Regions"))
	rightContent.WriteString("\n")
	maxRegions := min(8, len(m.regions))
	for i := 0; i < maxRegions; i++ {
		rightContent.WriteString(fmt.Sprintf("%s %s", keyStyle.Render(fmt.Sprintf("<%d>", i)), m.regions[i]))
		if i < maxRegions-1 {
			rightContent.WriteString("\n")
		}
	}
	
	// Add DOGOCTL ASCII art if space allows
	if m.width > 140 {
		rightContent.WriteString("\n\n")
		asciiArt := `
 ██████╗  ██████╗  ██████╗  ██████╗ ██████╗ ████████╗██╗
██╔═══██╗██╔═══██╗██╔═══██╗██╔════╝██╔═══██╗╚══██╔══╝██║
██║   ██║██║   ██║██║   ██║██║     ██║   ██║   ██║   ██║
██║   ██║██║   ██║██║   ██║██║     ██║   ██║   ██║   ██║
╚██████╔╝╚██████╔╝╚██████╔╝╚██████╗╚██████╔╝   ██║   ██║
 ╚═════╝  ╚═════╝  ╚═════╝  ╚═════╝ ╚═════╝    ╚═╝   ╚═╝`
		rightContent.WriteString(lipgloss.NewStyle().Foreground(primaryColor).Render(asciiArt))
	}

	rightPanel := lipgloss.NewStyle().
		Width(rightWidth).
		Padding(0, 1).
		Render(rightContent.String())

	// Join panels horizontally
	return lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, middlePanel, rightPanel)
}

// Two panel k9s-style for medium screens
func (m model) renderTopBarTwoPanelK9s() string {
	panelPadding := 4
	availableWidth := m.width - panelPadding
	// iTerm optimization: Ensure valid width
	if availableWidth < 70 {
		availableWidth = 70
	}
	// Ensure left panel has minimum width for full visibility (minimum 45 chars)
	leftPanelWidth := (availableWidth / 2) - 2
	if leftPanelWidth < 45 {
		leftPanelWidth = 45
	}
	rightPanelWidth := availableWidth - leftPanelWidth - 4
	// iTerm-optimized: Ensure right panel has enough width (minimum 25 chars for simple format)
	if rightPanelWidth < 25 {
		rightPanelWidth = 25
		leftPanelWidth = availableWidth - rightPanelWidth - 4
		if leftPanelWidth < 40 {
			// If left panel becomes too narrow, reduce right panel slightly but keep minimum
			leftPanelWidth = 40
			rightPanelWidth = availableWidth - leftPanelWidth - 4
			if rightPanelWidth < 20 {
				rightPanelWidth = 20 // Absolute minimum for iTerm
			}
		}
	}
	panelWidth := leftPanelWidth

	// Left panel - Context info (ensure all info is visible)
	var leftContent strings.Builder
	leftContent.WriteString(labelStyle.Render("Context: ") + valueStyle.Render("DigitalOcean"))
	leftContent.WriteString("\n")
	
	if m.currentView == "cluster-resources" && m.selectedCluster != nil {
		leftContent.WriteString(labelStyle.Render("Cluster: ") + valueStyle.Render(m.selectedCluster.Name))
		leftContent.WriteString("\n")
		leftContent.WriteString(labelStyle.Render("Region: ") + valueStyle.Render(m.selectedCluster.RegionSlug))
		leftContent.WriteString("\n")
		namespaceDisplay := "all"
		if m.selectedNamespace != "" {
			namespaceDisplay = m.selectedNamespace
		}
		leftContent.WriteString(labelStyle.Render("Namespace: ") + valueStyle.Render(namespaceDisplay))
		leftContent.WriteString("\n")
		leftContent.WriteString(labelStyle.Render("Resource: ") + valueStyle.Render(strings.Title(m.clusterResourceType)))
		leftContent.WriteString("\n")
		leftContent.WriteString(labelStyle.Render("Count: ") + valueStyle.Render(fmt.Sprintf("%d", len(m.clusterResources))))
	} else if m.currentView == "clusters" {
		leftContent.WriteString(labelStyle.Render("View: ") + valueStyle.Render("Kubernetes Clusters"))
		leftContent.WriteString("\n")
		leftContent.WriteString(labelStyle.Render("Clusters: ") + valueStyle.Render(fmt.Sprintf("%d", m.clusterCount)))
	} else {
		leftContent.WriteString(labelStyle.Render("Region: ") + valueStyle.Render(m.selectedRegion))
		leftContent.WriteString("\n")
		leftContent.WriteString(labelStyle.Render("Droplets: ") + valueStyle.Render(fmt.Sprintf("%d", m.dropletCount)))
	}
	
	refreshTime := "N/A"
	if !m.lastRefresh.IsZero() {
		refreshTime = m.lastRefresh.Format("15:04:05")
	}
	leftContent.WriteString("\n")
	leftContent.WriteString(labelStyle.Render("Refresh: ") + valueStyle.Render(refreshTime))
	
	if m.account != nil && m.currentView != "cluster-resources" {
		leftContent.WriteString("\n")
		leftContent.WriteString(labelStyle.Render("Status: ") + valueStyle.Render(m.account.Status))
	}

	// iTerm-optimized: Better padding for left panel
	leftPanel := lipgloss.NewStyle().
		Width(panelWidth).
		Padding(0, 2). // More padding for iTerm
		Render(leftContent.String())

	// Right panel - Keybindings (optimized for iTerm)
	// ALWAYS show view switching keys first - prioritize 1, 2, n
	var rightContent strings.Builder
	rightContent.WriteString(headerStyle.Render("Keys"))
	rightContent.WriteString("\n")
	
	// iTerm-optimized: Simple format matching the desired output
	// CRITICAL: Always show 1, 2, n first - simple format like "1 Droplets"
	if m.currentView == "droplets" {
		// Droplets view - show 1, 2, n prominently (matching image format)
		rightContent.WriteString(keyStyle.Render("1") + " Droplets\n")
		rightContent.WriteString(keyStyle.Render("2") + " Clusters\n")
		rightContent.WriteString(keyStyle.Render("n") + " New\n")
		rightContent.WriteString(keyStyle.Render("r") + " Refresh\n")
		rightContent.WriteString(keyStyle.Render("d") + " Delete\n")
		rightContent.WriteString(keyStyle.Render("s") + " SSH\n")
		rightContent.WriteString(keyStyle.Render("enter") + " Details\n")
		rightContent.WriteString(keyStyle.Render("?") + " Help\n")
		rightContent.WriteString(keyStyle.Render("q") + " Quit")
	} else if m.currentView == "clusters" {
		rightContent.WriteString(keyStyle.Render("1") + " Droplets\n")
		rightContent.WriteString(keyStyle.Render("2") + " Clusters\n")
		rightContent.WriteString(keyStyle.Render("r") + " Refresh\n")
		rightContent.WriteString(keyStyle.Render("enter") + " Enter\n")
		rightContent.WriteString(keyStyle.Render("q") + " Quit")
	} else {
		rightContent.WriteString(keyStyle.Render("1") + " Droplets\n")
		rightContent.WriteString(keyStyle.Render("2") + " Clusters\n")
		rightContent.WriteString(keyStyle.Render(":") + " Command\n")
		rightContent.WriteString(keyStyle.Render("d") + " Next\n")
		rightContent.WriteString(keyStyle.Render("n") + " Namespace\n")
		rightContent.WriteString(keyStyle.Render("r") + " Refresh\n")
		rightContent.WriteString(keyStyle.Render("esc") + " Back\n")
		rightContent.WriteString(keyStyle.Render("q") + " Quit")
	}

	// iTerm-optimized: Ensure panel has enough width (minimum 25 chars for simple format)
	minWidth := 25
	if rightPanelWidth < minWidth {
		rightPanelWidth = minWidth
	}
	
	// iTerm-friendly padding and rendering
	rightPanel := lipgloss.NewStyle().
		Width(rightPanelWidth).
		Padding(0, 1). // Standard padding for iTerm
		Render(rightContent.String())

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)
}

// Compact k9s-style for small screens
func (m model) renderTopBarCompactK9s() string {
	var s strings.Builder
	s.WriteString(headerStyle.Render("DigitalOcean"))
	s.WriteString(" | ")
	s.WriteString(labelStyle.Render("Droplets: ") + valueStyle.Render(fmt.Sprintf("%d", m.dropletCount)))
	s.WriteString(" | ")
	s.WriteString(labelStyle.Render("Region: ") + valueStyle.Render(m.selectedRegion))
	s.WriteString("\n")
	var keybindings string
	if m.currentView == "droplets" {
		keybindings = keyStyle.Render("<1>") + " Droplets | " + keyStyle.Render("<2>") + " Clusters | " + keyStyle.Render("<n>") + " New | " + keyStyle.Render("<r>") + " Refresh | " + keyStyle.Render("<d>") + " Delete | " + keyStyle.Render("<s>") + " SSH | " + keyStyle.Render("<q>") + " Quit"
	} else if m.currentView == "clusters" {
		keybindings = keyStyle.Render("<1>") + " Droplets | " + keyStyle.Render("<2>") + " Clusters | " + keyStyle.Render("<r>") + " Refresh | " + keyStyle.Render("<enter>") + " Enter | " + keyStyle.Render("<q>") + " Quit"
	} else {
		keybindings = keyStyle.Render("<1>") + " Droplets | " + keyStyle.Render("<2>") + " Clusters | " + keyStyle.Render("<:>") + " Command | " + keyStyle.Render("<d>") + " Next | " + keyStyle.Render("<n>") + " Namespace | " + keyStyle.Render("<r>") + " Refresh | " + keyStyle.Render("<esc>") + " Back | " + keyStyle.Render("<q>") + " Quit"
	}
	s.WriteString(keybindings)
	return s.String()
}

func (m model) renderTopBarThreePanel() string {
	// Three panel layout for large screens
	panelPadding := 6
	availableWidth := m.width - panelPadding
	if availableWidth < 100 {
		return m.renderTopBarTwoPanel()
	}

	panelWidth := (availableWidth / 3) - 2

	// Ensure minimum width - if too small, fall back to two-panel
	if panelWidth < 35 {
		return m.renderTopBarTwoPanel()
	}

	// Left panel - Summary (ALWAYS VISIBLE)
	var accountInfo strings.Builder
	accountInfo.WriteString(headerStyle.Render("DigitalOcean"))
	accountInfo.WriteString("\n")

	// ALWAYS show essential summary info - this is the core summary
	accountInfo.WriteString(labelStyle.Render("Droplets: ") + valueStyle.Render(fmt.Sprintf("%d", m.dropletCount)))
	accountInfo.WriteString("\n")
	accountInfo.WriteString(labelStyle.Render("Region: ") + valueStyle.Render(m.selectedRegion))
	accountInfo.WriteString("\n")

	refreshTime := "N/A"
	if !m.lastRefresh.IsZero() {
		refreshTime = m.lastRefresh.Format("15:04:05")
	}
	accountInfo.WriteString(labelStyle.Render("Refresh: ") + valueStyle.Render(refreshTime))
	accountInfo.WriteString("\n")

	// Show account info if available
	if m.account != nil {
		email := truncateString(m.account.Email, panelWidth-15)
		if email != "" {
			accountInfo.WriteString(labelStyle.Render("Account: ") + valueStyle.Render(email))
			accountInfo.WriteString("\n")
		}
		accountInfo.WriteString(labelStyle.Render("Status: ") + valueStyle.Render(m.account.Status))
		accountInfo.WriteString("\n")
	}

	accountInfo.WriteString(labelStyle.Render("Version: ") + valueStyle.Render("dogoctl v1.0"))

	// Render the left panel - ensure it's always visible with summary
	leftPanelContent := accountInfo.String()
	// CRITICAL: Ensure content is not empty
	if strings.TrimSpace(leftPanelContent) == "" {
		leftPanelContent = fmt.Sprintf("DigitalOcean\n\nDroplets: %d\nRegion: %s\nRefresh: N/A\nVersion: dogoctl v1.1.0",
			m.dropletCount, m.selectedRegion)
	}
	// Render with proper width - ensure content is visible
	leftPanel := panelStyle.Width(panelWidth).Render(leftPanelContent)
	// Double-check panel is not empty
	if strings.TrimSpace(leftPanel) == "" {
		leftPanel = panelStyle.Width(panelWidth).Render(fmt.Sprintf("DigitalOcean\n\nDroplets: %d\nRegion: %s",
			m.dropletCount, m.selectedRegion))
	}

	// Middle panel - Keybindings
	keybindings := []string{
		keyStyle.Render("<?>") + " Help",
		keyStyle.Render("<ctrl-d>") + " Delete",
		keyStyle.Render("<n>") + " New",
		keyStyle.Render("<r>") + " Refresh",
		keyStyle.Render("<enter>") + " Details",
		keyStyle.Render("<q>") + " Quit",
	}

	middlePanelContent := headerStyle.Render("Keybindings") + "\n" + strings.Join(keybindings, "\n")
	middlePanel := panelStyle.
		Width(panelWidth).
		Render(middlePanelContent)

	// Right panel - Region filters
	regionFilters := []string{headerStyle.Render("Regions")}
	maxRegions := min(9, len(m.regions))

	for i := 0; i < maxRegions; i++ {
		regionFilters = append(regionFilters, fmt.Sprintf("%s %s", keyStyle.Render(fmt.Sprintf("<%d>", i)), m.regions[i]))
	}

	// Add DOGOCTL ASCII art only if screen is wide enough
	var rightPanelContent strings.Builder
	rightPanelContent.WriteString(strings.Join(regionFilters, "\n"))
	if m.width > 150 {
		asciiArt := `
 ██████╗  ██████╗  ██████╗  ██████╗ ██████╗ ████████╗██╗
██╔═══██╗██╔═══██╗██╔═══██╗██╔════╝██╔═══██╗╚══██╔══╝██║
██║   ██║██║   ██║██║   ██║██║     ██║   ██║   ██║   ██║
██║   ██║██║   ██║██║   ██║██║     ██║   ██║   ██║   ██║
╚██████╔╝╚██████╔╝╚██████╔╝╚██████╗╚██████╔╝   ██║   ██║
 ╚═════╝  ╚═════╝  ╚═════╝  ╚═════╝ ╚═════╝    ╚═╝   ╚═╝`
		rightPanelContent.WriteString("\n")
		rightPanelContent.WriteString(lipgloss.NewStyle().Foreground(warningColor).Render(asciiArt))
	}

	rightPanel := panelStyle.
		Width(panelWidth).
		Render(rightPanelContent.String())

	// Join panels horizontally - ensure all are visible
	return lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, middlePanel, rightPanel)
}

func (m model) renderTopBarVertical() string {
	// Vertical layout for small screens (70-100 chars)
	// ALWAYS show essential info
	var s strings.Builder
	s.WriteString(headerStyle.Render("DigitalOcean"))
	s.WriteString("\n")
	s.WriteString(labelStyle.Render("Droplets: ") + valueStyle.Render(fmt.Sprintf("%d", m.dropletCount)))
	s.WriteString(" | ")
	s.WriteString(labelStyle.Render("Region: ") + valueStyle.Render(m.selectedRegion))

	refreshTime := "N/A"
	if !m.lastRefresh.IsZero() {
		refreshTime = m.lastRefresh.Format("15:04:05")
	}
	s.WriteString("\n")
	s.WriteString(labelStyle.Render("Refresh: ") + valueStyle.Render(refreshTime))
	s.WriteString(" | ")
	s.WriteString(keyStyle.Render("n") + " New | " + keyStyle.Render("r") + " Refresh | " + keyStyle.Render("d") + " Delete | " + keyStyle.Render("s") + " SSH | " + keyStyle.Render("q") + " Quit")
	result := s.String()
	// Ensure we return something
	if strings.TrimSpace(result) == "" {
		result = fmt.Sprintf("DigitalOcean\nDroplets: %d | Region: %s\nRefresh: %s", m.dropletCount, m.selectedRegion, refreshTime)
	}
	return result
}

// Removed duplicate renderTopBarVertical_OLD function - no longer needed

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return "..."
	}
	return s[:maxLen-3] + "..."
}

func (m model) renderStatusBar() string {
	// k9s-style footer showing current view type
	var statusText string
	if m.currentView == "cluster-resources" {
		// Show cluster resource view
		if m.selectedCluster != nil {
			statusText = fmt.Sprintf("<%s>", m.clusterResourceType)
			statusText = fmt.Sprintf("%s | Cluster: %s | Resource: %s", statusText, m.selectedCluster.Name, strings.Title(m.clusterResourceType))
		}
	} else if m.currentView == "clusters" {
		statusText = fmt.Sprintf("<clusters>")
		statusText = fmt.Sprintf("%s | Clusters [%d]", statusText, m.clusterCount)
	} else {
		statusText = fmt.Sprintf("<droplets>")
		if m.selectedRegion != "all" {
			statusText = fmt.Sprintf("<droplets> [%s]", m.selectedRegion)
		}
		statusText = fmt.Sprintf("%s | Droplets(%s) [%d]", statusText, m.selectedRegion, m.dropletCount)
	}
	
	if m.loading {
		statusText = fmt.Sprintf("%s | %s Loading...", statusText, m.spinner.View())
	}
	
	// Make status bar responsive to width
	width := m.width
	if width < 40 {
		width = 40
	}
	
	// Truncate if too long
	maxStatusLen := width - 4
	if len(statusText) > maxStatusLen {
		statusText = statusText[:maxStatusLen-3] + "..."
	}
	
	return lipgloss.NewStyle().
		Foreground(mutedColor).
		Background(bgColor).
		Padding(0, 1).
		Width(width).
		Render(statusText)
}

func (m model) renderDeleteConfirmation() string {
	var s strings.Builder
	s.WriteString("\n")

	// Dynamic box width based on terminal size
	boxWidth := min(m.width-4, 60)
	if boxWidth < 40 {
		boxWidth = 40
	}
	if boxWidth > m.width-4 {
		boxWidth = m.width - 4
	}

	warningBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(warningColor).
		Padding(1, 2).
		Width(boxWidth).
		Render(
			fmt.Sprintf(
				"⚠️  Delete Droplet?\n\n"+
					"Droplet: %s\nID: %d\n\n"+
					"This action cannot be undone!\n\n"+
					"[y] Yes, delete  [n] No, cancel",
				truncateString(m.deleteTargetName, boxWidth-10),
				m.deleteTargetID,
			),
		)

	s.WriteString(lipgloss.PlaceHorizontal(m.width, lipgloss.Center, warningBox))
	s.WriteString("\n")

	return s.String()
}

func (m model) renderCreateForm() string {
	var s strings.Builder
	s.WriteString("\n")

	formTitle := headerStyle.Render("✨ Create New Droplet")
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

	return s.String()
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

	// Dynamic box width based on terminal size
	boxWidth := min(m.width-4, 70)
	if boxWidth < 50 {
		boxWidth = 50
	}
	if boxWidth > m.width-4 {
		boxWidth = m.width - 4
	}

	headerBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 2).
		Width(boxWidth).
		Render(headerText)

	s.WriteString(headerBox)
	s.WriteString("\n\n")

	// Details
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

	// Render details - dynamic width based on terminal size
	detailsBoxWidth := min(m.width-4, 70)
	if detailsBoxWidth < 50 {
		detailsBoxWidth = 50
	}
	if detailsBoxWidth > m.width-4 {
		detailsBoxWidth = m.width - 4
	}

	detailsBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 2).
		Width(detailsBoxWidth)
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

	return s.String()
}

func (m model) renderClusterDetails() string {
	if m.selectedCluster == nil {
		return ""
	}

	c := m.selectedCluster
	var s strings.Builder

	// Header with status
	status := string(c.Status.State)
	statusColor := successColor
	statusIcon := "●"
	if status == "degraded" || status == "error" {
		statusColor = errorColor
		statusIcon = "○"
	} else if status == "provisioning" || status == "running_setup" {
		statusColor = warningColor
		statusIcon = "◐"
	}

	statusStyle := lipgloss.NewStyle().Foreground(statusColor).Bold(true)
	headerText := fmt.Sprintf("☸️  %s  %s", c.Name, statusStyle.Render(fmt.Sprintf("%s %s", statusIcon, strings.ToUpper(status))))

	// Dynamic box width based on terminal size
	boxWidth := min(m.width-4, 70)
	if boxWidth < 50 {
		boxWidth = 50
	}
	if boxWidth > m.width-4 {
		boxWidth = m.width - 4
	}

	headerBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 2).
		Width(boxWidth).
		Render(headerText)

	s.WriteString(headerBox)
	s.WriteString("\n\n")

	// Details
	labelStyle := lipgloss.NewStyle().Foreground(primaryColor).Bold(true).Width(15)
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("255"))

	// Count nodes
	totalNodes := 0
	for _, np := range c.NodePools {
		totalNodes += np.Count
	}

	createdAt := "N/A"
	if !c.CreatedAt.IsZero() {
		createdAt = c.CreatedAt.Format("2006-01-02 15:04:05")
	}

	details := []struct {
		label string
		value string
	}{
		{"🆔 ID:", c.ID},
		{"📍 Region:", c.RegionSlug},
		{"📦 Version:", c.VersionSlug},
		{"🏊 Node Pools:", fmt.Sprintf("%d", len(c.NodePools))},
		{"🖥️  Total Nodes:", fmt.Sprintf("%d", totalNodes)},
		{"📅 Created:", createdAt},
		{"🔗 Endpoint:", c.Endpoint},
	}

	detailsText := ""
	for _, d := range details {
		detailsText += fmt.Sprintf("%s %s\n", labelStyle.Render(d.label), valueStyle.Render(d.value))
	}

	// Node Pools details
	if len(c.NodePools) > 0 {
		detailsText += "\n" + labelStyle.Render("Node Pools:") + "\n"
		for i, np := range c.NodePools {
			detailsText += fmt.Sprintf("  %d. %s - %d nodes (%s)\n", i+1, np.Name, np.Count, np.Size)
		}
	}

	// Render details box
	detailsBoxWidth := min(m.width-4, 70)
	if detailsBoxWidth < 50 {
		detailsBoxWidth = 50
	}
	if detailsBoxWidth > m.width-4 {
		detailsBoxWidth = m.width - 4
	}

	detailsBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 2).
		Width(detailsBoxWidth)

	s.WriteString(detailsBox.Render(detailsText))
	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Press ESC, ENTER, or BACKSPACE to return"))

	return s.String()
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

func loadClusters(client *godo.Client) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		opt := &godo.ListOptions{PerPage: 200}
		clusters, _, err := client.Kubernetes.List(ctx, opt)
		if err != nil {
			return errMsg(err)
		}
		return clustersLoadedMsg(clusters)
	}
}

func loadClusterResources(client *godo.Client, cluster *godo.KubernetesCluster, resourceType string, namespace string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		
		// Get kubeconfig for the cluster
		kubeconfigResp, _, err := client.Kubernetes.GetKubeConfig(ctx, cluster.ID)
		if err != nil {
			return errMsg(fmt.Errorf("failed to get kubeconfig: %v", err))
		}
		
		// Kubeconfig is already bytes, no need to decode
		kubeconfigBytes := kubeconfigResp.KubeconfigYAML
		
		// Parse kubeconfig
		config, err := clientcmd.Load(kubeconfigBytes)
		if err != nil {
			return errMsg(fmt.Errorf("failed to parse kubeconfig: %v", err))
		}
		
		// Create client config
		clientConfig := clientcmd.NewDefaultClientConfig(*config, &clientcmd.ConfigOverrides{})
		restConfig, err := clientConfig.ClientConfig()
		if err != nil {
			return errMsg(fmt.Errorf("failed to create client config: %v", err))
		}
		
		// Create Kubernetes client
		k8sClient, err := kubernetes.NewForConfig(restConfig)
		if err != nil {
			return errMsg(fmt.Errorf("failed to create k8s client: %v", err))
		}
		
		// Fetch resources based on type
		resources := []map[string]interface{}{}
		
		// Determine namespace - empty string means all namespaces
		ns := namespace
		if ns == "" {
			ns = metav1.NamespaceAll
		}
		
		switch resourceType {
		case "deployments":
			deployments, err := k8sClient.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
			if err == nil {
				for _, d := range deployments.Items {
					readyReplicas := d.Status.ReadyReplicas
					replicas := d.Status.Replicas
					age := "N/A"
					if !d.CreationTimestamp.IsZero() {
						duration := time.Since(d.CreationTimestamp.Time)
						if duration.Hours() < 24 {
							age = fmt.Sprintf("%.0fh", duration.Hours())
						} else {
							age = fmt.Sprintf("%.0fd", duration.Hours()/24)
						}
					}
					resources = append(resources, map[string]interface{}{
						"name":      d.Name,
						"namespace": d.Namespace,
						"ready":     fmt.Sprintf("%d/%d", readyReplicas, replicas),
						"upToDate":  fmt.Sprintf("%d", d.Status.UpdatedReplicas),
						"available": fmt.Sprintf("%d", d.Status.AvailableReplicas),
						"age":       age,
					})
				}
			}
		case "daemonsets":
			daemonsets, err := k8sClient.AppsV1().DaemonSets(ns).List(ctx, metav1.ListOptions{})
			if err == nil {
				for _, ds := range daemonsets.Items {
					readyReplicas := ds.Status.NumberReady
					desiredReplicas := ds.Status.DesiredNumberScheduled
					age := "N/A"
					if !ds.CreationTimestamp.IsZero() {
						duration := time.Since(ds.CreationTimestamp.Time)
						if duration.Hours() < 24 {
							age = fmt.Sprintf("%.0fh", duration.Hours())
						} else {
							age = fmt.Sprintf("%.0fd", duration.Hours()/24)
						}
					}
					resources = append(resources, map[string]interface{}{
						"name":      ds.Name,
						"namespace": ds.Namespace,
						"ready":     fmt.Sprintf("%d/%d", readyReplicas, desiredReplicas),
						"current":   fmt.Sprintf("%d", ds.Status.CurrentNumberScheduled),
						"age":       age,
					})
				}
			}
		case "statefulsets":
			statefulsets, err := k8sClient.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
			if err == nil {
				for _, sts := range statefulsets.Items {
					readyReplicas := sts.Status.ReadyReplicas
					replicas := *sts.Spec.Replicas
					age := "N/A"
					if !sts.CreationTimestamp.IsZero() {
						duration := time.Since(sts.CreationTimestamp.Time)
						if duration.Hours() < 24 {
							age = fmt.Sprintf("%.0fh", duration.Hours())
						} else {
							age = fmt.Sprintf("%.0fd", duration.Hours()/24)
						}
					}
					resources = append(resources, map[string]interface{}{
						"name":      sts.Name,
						"namespace": sts.Namespace,
						"ready":     fmt.Sprintf("%d/%d", readyReplicas, replicas),
						"age":       age,
					})
				}
			}
		case "pvc":
			pvcs, err := k8sClient.CoreV1().PersistentVolumeClaims(ns).List(ctx, metav1.ListOptions{})
			if err == nil {
				for _, pvc := range pvcs.Items {
					age := "N/A"
					if !pvc.CreationTimestamp.IsZero() {
						duration := time.Since(pvc.CreationTimestamp.Time)
						if duration.Hours() < 24 {
							age = fmt.Sprintf("%.0fh", duration.Hours())
						} else {
							age = fmt.Sprintf("%.0fd", duration.Hours()/24)
						}
					}
					status := string(pvc.Status.Phase)
					capacity := "N/A"
					if len(pvc.Status.Capacity) > 0 {
						if storage, ok := pvc.Status.Capacity["storage"]; ok {
							capacity = storage.String()
						}
					}
					resources = append(resources, map[string]interface{}{
						"name":      pvc.Name,
						"namespace": pvc.Namespace,
						"status":    status,
						"capacity":  capacity,
						"age":       age,
					})
				}
			}
		case "configmaps":
			configmaps, err := k8sClient.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{})
			if err == nil {
				for _, cm := range configmaps.Items {
					age := "N/A"
					if !cm.CreationTimestamp.IsZero() {
						duration := time.Since(cm.CreationTimestamp.Time)
						if duration.Hours() < 24 {
							age = fmt.Sprintf("%.0fh", duration.Hours())
						} else {
							age = fmt.Sprintf("%.0fd", duration.Hours()/24)
						}
					}
					resources = append(resources, map[string]interface{}{
						"name":      cm.Name,
						"namespace": cm.Namespace,
						"data":      fmt.Sprintf("%d", len(cm.Data)),
						"age":       age,
					})
				}
			}
		case "secrets":
			secrets, err := k8sClient.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{})
			if err == nil {
				for _, sec := range secrets.Items {
					age := "N/A"
					if !sec.CreationTimestamp.IsZero() {
						duration := time.Since(sec.CreationTimestamp.Time)
						if duration.Hours() < 24 {
							age = fmt.Sprintf("%.0fh", duration.Hours())
						} else {
							age = fmt.Sprintf("%.0fd", duration.Hours()/24)
						}
					}
					secretType := string(sec.Type)
					resources = append(resources, map[string]interface{}{
						"name":      sec.Name,
						"namespace": sec.Namespace,
						"type":      secretType,
						"data":      fmt.Sprintf("%d", len(sec.Data)),
						"age":       age,
					})
				}
			}
		case "pods":
			pods, err := k8sClient.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
			if err == nil {
				for _, p := range pods.Items {
					ready := 0
					total := len(p.Spec.Containers)
					for _, cs := range p.Status.ContainerStatuses {
						if cs.Ready {
							ready++
						}
					}
					age := "N/A"
					if !p.CreationTimestamp.IsZero() {
						duration := time.Since(p.CreationTimestamp.Time)
						if duration.Hours() < 24 {
							age = fmt.Sprintf("%.0fh", duration.Hours())
						} else {
							age = fmt.Sprintf("%.0fd", duration.Hours()/24)
						}
					}
					resources = append(resources, map[string]interface{}{
						"name":      p.Name,
						"namespace": p.Namespace,
						"ready":     fmt.Sprintf("%d/%d", ready, total),
						"status":    string(p.Status.Phase),
						"restarts":  fmt.Sprintf("%d", p.Status.ContainerStatuses[0].RestartCount),
						"age":       age,
					})
				}
			}
		case "services":
			services, err := k8sClient.CoreV1().Services(ns).List(ctx, metav1.ListOptions{})
			if err == nil {
				for _, s := range services.Items {
					age := "N/A"
					if !s.CreationTimestamp.IsZero() {
						duration := time.Since(s.CreationTimestamp.Time)
						if duration.Hours() < 24 {
							age = fmt.Sprintf("%.0fh", duration.Hours())
						} else {
							age = fmt.Sprintf("%.0fd", duration.Hours()/24)
						}
					}
					externalIP := "<none>"
					if len(s.Status.LoadBalancer.Ingress) > 0 {
						externalIP = s.Status.LoadBalancer.Ingress[0].IP
					}
					resources = append(resources, map[string]interface{}{
						"name":       s.Name,
						"namespace":  s.Namespace,
						"type":       string(s.Spec.Type),
						"clusterIP":  s.Spec.ClusterIP,
						"externalIP": externalIP,
						"age":        age,
					})
				}
			}
		case "nodes":
			nodes, err := k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			if err == nil {
				for _, n := range nodes.Items {
					age := "N/A"
					if !n.CreationTimestamp.IsZero() {
						duration := time.Since(n.CreationTimestamp.Time)
						if duration.Hours() < 24 {
							age = fmt.Sprintf("%.0fh", duration.Hours())
						} else {
							age = fmt.Sprintf("%.0fd", duration.Hours()/24)
						}
					}
					status := "NotReady"
					for _, condition := range n.Status.Conditions {
						if condition.Type == "Ready" && condition.Status == "True" {
							status = "Ready"
							break
						}
					}
					roles := "<none>"
					if len(n.Labels["node-role.kubernetes.io/master"]) > 0 {
						roles = "master"
					}
					resources = append(resources, map[string]interface{}{
						"name":     n.Name,
						"status":   status,
						"roles":    roles,
						"age":      age,
						"version":  n.Status.NodeInfo.KubeletVersion,
					})
				}
			}
		case "namespaces":
			namespaces, err := k8sClient.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
			if err == nil {
				for _, ns := range namespaces.Items {
					age := "N/A"
					if !ns.CreationTimestamp.IsZero() {
						duration := time.Since(ns.CreationTimestamp.Time)
						if duration.Hours() < 24 {
							age = fmt.Sprintf("%.0fh", duration.Hours())
						} else {
							age = fmt.Sprintf("%.0fd", duration.Hours()/24)
						}
					}
					status := "Active"
					if ns.Status.Phase != "" {
						status = string(ns.Status.Phase)
					}
					resources = append(resources, map[string]interface{}{
						"name":   ns.Name,
						"status": status,
						"age":    age,
					})
				}
			}
		}
		
		return clusterResourcesLoadedMsg{
			resourceType: resourceType,
			resources:    resources,
		}
	}
}

func loadAccountInfo(client *godo.Client) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		account, _, err := client.Account.Get(ctx)
		if err != nil {
			return errMsg(err)
		}
		return accountInfoMsg{account: account}
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

// executeSSH executes an SSH connection to the droplet
// This should be called after the tea program exits
func executeSSH(ip, name string) error {
	fmt.Printf("\n🔌 Connecting to %s (%s)...\n\n", name, ip)
	
	// SSH command
	cmd := exec.Command("ssh", ip)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	// Execute SSH (this will block until SSH session ends)
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("SSH connection to %s (%s) failed: %v", name, ip, err)
	}
	
	return nil
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

	// Main loop: restart TUI after SSH sessions
	// iTerm optimization: Use standard output and ensure proper terminal detection
	for {
		m := initialModel(client)
		// iTerm-optimized: Ensure proper terminal capabilities
		// iTerm optimization: Use standard alt screen, bubbletea handles terminal detection
		p := tea.NewProgram(m, tea.WithAltScreen())

		if err := p.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
			os.Exit(1)
		}
		
		// If SSH was requested, execute it now (after program exits)
		if sshIP != "" {
			ip := sshIP
			name := sshName
			// Clear SSH info before executing (so we can restart TUI after)
			sshIP = ""
			sshName = ""
			
			if err := executeSSH(ip, name); err != nil {
				fmt.Fprintf(os.Stderr, "❌ %v\n", err)
				// Continue loop to restart TUI even if SSH fails
			}
			// After SSH exits, continue loop to restart TUI
			continue
		}
		
		// If no SSH was requested, exit normally
		break
	}
}
