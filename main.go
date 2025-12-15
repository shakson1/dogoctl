package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/cliofy/govte"
	"github.com/cliofy/govte/terminal"
	"github.com/creack/pty/v2"
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
	table               table.Model
	client              *godo.Client
	droplets            []godo.Droplet
	clusters            []*godo.KubernetesCluster
	clusterResources    []map[string]interface{} // Resources from selected cluster
	account             *godo.Account
	creating            bool
	viewingDetails      bool
	confirmDelete       bool
	deleteTargetID      int
	deleteTargetName    string
	loading             bool
	spinner             spinner.Model
	selectedDroplet     *godo.Droplet
	selectedCluster     *godo.KubernetesCluster
	currentView         string // "droplets", "clusters", or "cluster-resources"
	clusterResourceType string // "deployments", "pods", "services", "nodes", etc.
	selectedNamespace   string // Current namespace filter (empty = all namespaces)
	commandMode         bool   // Command input mode (like k9s :command)
	commandInput        textinput.Model
	nameInput           textinput.Model
	regionInput         textinput.Model
	sizeInput           textinput.Model
	imageInput          textinput.Model
	tagsInput           textinput.Model
	inputIndex          int
	err                 error
	successMsg          string
	dropletCount        int
	clusterCount        int
	lastRefresh         time.Time
	selectedRegion      string
	regions             []string
	width               int
	height              int
	selectingSSHIP      bool   // When true, show IP selection menu for SSH
	sshIPType           string // "public" or "private" - selected IP type for SSH
	// Create form selection state
	selectingRegion    bool // When true, show region selection table
	selectingSize      bool // When true, show size selection table
	selectingImage     bool // When true, show image selection table
	availableRegions   []godo.Region
	availableSizes     []godo.Size
	availableImages    []godo.Image
	selectedRegionSlug string      // Selected region slug for creation
	selectedSizeSlug   string      // Selected size slug for creation
	selectedImageSlug  string      // Selected image slug for creation
	selectionTable     table.Model // Table for selecting region/size/image
	// Billing dashboard state
	billingBalance        *godo.Balance
	billingInvoices       []godo.InvoiceListItem
	billingHistory        *godo.BillingHistory
	billingMode           string                    // "invoices" or "monthly" - which view to show
	selectedBillingMonth  string                    // Selected month for detailed view (format: "YYYY-MM")
	viewingBillingDetails bool                      // When true, show detailed billing information
	selectedInvoice       *godo.InvoiceListItem     // Selected invoice for details
	selectedBillingEntry  *godo.BillingHistoryEntry // Selected billing entry for details
	detailedInvoice       *godo.Invoice             // Full invoice details loaded from API
	billingDetailsScroll  int                       // Scroll position for billing details view
	// SSH terminal state
	sshTerminalActive      bool                     // When true, show SSH terminal view
	sshTerminalRawOutput   *strings.Builder         // Raw terminal output buffer (for debugging only, not used for display)
	sshTerminalEmulator    *terminal.TerminalBuffer // Terminal emulator - SINGLE SOURCE OF TRUTH for rendering
	sshTerminalParser      *govte.Parser            // Parser for ANSI escape sequences
	sshTerminalPTY         *os.File                 // PTY file for SSH connection
	sshTerminalCmd         *exec.Cmd                // SSH command process
	sshTerminalHost        string                   // Connected host name
	sshTerminalIP          string                   // Connected IP address
	sshTerminalMutex       sync.Mutex               // Mutex for thread-safe terminal output access
	sshOutputChan          chan tea.Msg             // Channel for SSH output messages
	sshTerminalConfirmExit bool                     // When true, show exit confirmation dialog
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
type regionsLoadedMsg []godo.Region
type sizesLoadedMsg []godo.Size
type imagesLoadedMsg []godo.Image
type balanceLoadedMsg *godo.Balance
type invoicesLoadedMsg []godo.InvoiceListItem
type billingHistoryLoadedMsg *godo.BillingHistory
type invoiceDetailsLoadedMsg *godo.Invoice
type sshTerminalOutputMsg string // New line of output from SSH terminal

const (
	viewDroplets         = "droplets"
	viewClusters         = "clusters"
	viewClusterResources = "cluster-resources"
	viewBilling          = "billing"
)

// getTopPadding returns the number of rows to reserve at the top to avoid row 0
// This is applied globally at the root View() level, not in individual widgets
func getTopPadding() int {
	// Check for environment variable override first
	if envPadding := os.Getenv("DOGOCTL_TOP_PADDING"); envPadding != "" {
		if padding, err := strconv.Atoi(envPadding); err == nil && padding >= 0 {
			return padding
		}
	}

	// Check terminal type
	termProgram := os.Getenv("TERM_PROGRAM")
	if termProgram == "iTerm.app" {
		return 4 // iTerm2 needs 4 rows for safe rendering (row 0 is under chrome)
	}

	// Default for other terminals
	return 1
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

	// SSH connection info (deprecated - now using model fields)
	// sshIP   string
	// sshName string

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
		table:               t,
		client:              client,
		droplets:            []godo.Droplet{},
		clusters:            []*godo.KubernetesCluster{},
		clusterResources:    []map[string]interface{}{},
		account:             nil,
		creating:            false,
		viewingDetails:      false,
		confirmDelete:       false,
		loading:             false,
		spinner:             sp,
		selectedDroplet:     nil,
		selectedCluster:     nil,
		currentView:         viewDroplets,  // Start with droplets view
		clusterResourceType: "deployments", // Default resource type when entering cluster
		selectedNamespace:   "",            // Empty = all namespaces
		commandMode:         false,
		commandInput:        commandInput,
		nameInput:           nameInput,
		regionInput:         regionInput,
		sizeInput:           sizeInput,
		imageInput:          imageInput,
		tagsInput:           tagsInput,
		inputIndex:          0,
		dropletCount:        0,
		clusterCount:        0,
		lastRefresh:         time.Now(),
		selectedRegion:      "all",
		regions:             []string{"all"},
		width:               120,
		height:              40,
		selectingSSHIP:      false,
		sshIPType:           "public",
		selectingRegion:     false,
		selectingSize:       false,
		selectingImage:      false,
		availableRegions:    []godo.Region{},
		availableSizes:      []godo.Size{},
		availableImages:     []godo.Image{},
		selectedRegionSlug:  "",
		selectedSizeSlug:    "",
		selectedImageSlug:   "",
		selectionTable: func() table.Model {
			selTable := table.New(
				table.WithFocused(true),
				table.WithHeight(10),
			)
			selStyles := table.DefaultStyles()
			selStyles.Header = selStyles.Header.
				BorderStyle(lipgloss.NormalBorder()).
				BorderForeground(borderColor).
				BorderBottom(true).
				Bold(true).
				Foreground(primaryColor)
			selStyles.Selected = selStyles.Selected.
				Foreground(lipgloss.Color("229")).
				Background(lipgloss.Color("57")).
				Bold(true)
			selTable.SetStyles(selStyles)
			return selTable
		}(),
		billingBalance:         nil,
		billingInvoices:        []godo.InvoiceListItem{},
		billingHistory:         nil,
		billingMode:            "invoices", // Default to invoices view
		selectedBillingMonth:   "",
		viewingBillingDetails:  false,
		selectedInvoice:        nil,
		selectedBillingEntry:   nil,
		detailedInvoice:        nil,
		billingDetailsScroll:   0,
		sshTerminalActive:      false,
		sshTerminalRawOutput:   &strings.Builder{}, // Use pointer to avoid copy issues
		sshTerminalEmulator:    nil,
		sshTerminalParser:      nil,
		sshTerminalPTY:         nil,
		sshTerminalCmd:         nil,
		sshTerminalHost:        "",
		sshTerminalIP:          "",
		sshOutputChan:          make(chan tea.Msg, 100), // Buffered channel for SSH output
		sshTerminalConfirmExit: false,                   // No confirmation dialog initially
	}
}

func (m model) Init() tea.Cmd {
	// Set loading state to show spinner while fetching data
	m.loading = true
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

		// Handle SSH terminal mode - all input goes to SSH terminal emulator
		if m.sshTerminalActive {
			return m.updateSSHTerminal(msg)
		}

		// Handle SSH IP selection menu
		if m.selectingSSHIP {
			return m.updateSSHIPSelection(msg)
		}

		if m.confirmDelete {
			return m.updateDeleteConfirmation(msg)
		}

		if m.creating {
			return m.updateCreateForm(msg)
		}

		if m.viewingBillingDetails {
			key := msg.String()
			switch {
			case key == "esc" || key == "enter" || key == "backspace":
				m.viewingBillingDetails = false
				m.selectedInvoice = nil
				m.selectedBillingEntry = nil
				m.detailedInvoice = nil
				m.billingDetailsScroll = 0
				return m, nil
			case key == "up" || key == "k" || key == "K":
				// Scroll up
				if m.billingDetailsScroll > 0 {
					m.billingDetailsScroll--
				}
				return m, nil
			case key == "down" || key == "j" || key == "J":
				// Scroll down
				m.billingDetailsScroll++
				return m, nil
			case key == "pageup" || key == "ctrl+b":
				// Page up
				m.billingDetailsScroll = max(0, m.billingDetailsScroll-10)
				return m, nil
			case key == "pagedown" || key == "ctrl+f":
				// Page down
				m.billingDetailsScroll += 10
				return m, nil
			case key == "home" || key == "g":
				// Go to top
				m.billingDetailsScroll = 0
				return m, nil
			case key == "end" || key == "G":
				// Go to bottom (will be limited by content height in render)
				m.billingDetailsScroll = 9999
				return m, nil
			case key == "ctrl+c" || key == "q":
				return m, tea.Quit
			}
			// Also check key type for arrow keys
			switch msg.Type {
			case tea.KeyUp:
				if m.billingDetailsScroll > 0 {
					m.billingDetailsScroll--
				}
				return m, nil
			case tea.KeyDown:
				m.billingDetailsScroll++
				return m, nil
			case tea.KeyPgUp:
				m.billingDetailsScroll = max(0, m.billingDetailsScroll-10)
				return m, nil
			case tea.KeyPgDown:
				m.billingDetailsScroll += 10
				return m, nil
			case tea.KeyHome:
				m.billingDetailsScroll = 0
				return m, nil
			case tea.KeyEnd:
				m.billingDetailsScroll = 9999
				return m, nil
			}
			return m, nil
		}

		if m.viewingDetails {
			switch msg.String() {
			case "s", "S":
				// SSH into droplet from details view
				if m.selectedDroplet != nil {
					d := m.selectedDroplet
					// Check if droplet is active
					if d.Status != "active" {
						m.err = fmt.Errorf("droplet %s is not active (status: %s)", d.Name, d.Status)
						return m, nil
					}

					publicIP := getPublicIP(*d)
					privateIP := getPrivateIP(*d)

					if publicIP == "" && privateIP == "" {
						m.err = fmt.Errorf("droplet %s has no IP address", d.Name)
						return m, nil
					}

					// If both IPs exist, show selection menu
					if publicIP != "" && privateIP != "" {
						m.selectingSSHIP = true
						m.viewingDetails = false // Exit details view to show SSH selection menu
						return m, nil
					}

					// If only one IP type exists, use it directly
					var ip string
					if publicIP != "" {
						ip = publicIP
					} else {
						ip = privateIP
					}

					// Start SSH terminal view
					return m.startSSHTerminalView(ip, d.Name)
				}
				return m, nil
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
			if m.currentView == viewClusterResources {
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
			m.currentView = viewDroplets
			m.loading = true
			// Update table immediately with droplet columns
			m.updateTableRows()
			return m, tea.Batch(loadDroplets(m.client), m.spinner.Tick)
		case "2":
			// Switch to clusters view
			m.currentView = viewClusters
			m.loading = true
			// Update table immediately with cluster columns
			m.updateTableRows()
			return m, tea.Batch(loadClusters(m.client), m.spinner.Tick)
		case "3":
			// Handle "3" based on current view
			if m.currentView == viewDroplets {
				// In droplets view, "3" is for region filter
				idx := 3
				if idx > 0 && idx <= len(m.regions) {
					m.selectedRegion = m.regions[idx-1]
					m.updateTableRows()
				}
				return m, nil
			}
			// In other views, "3" switches to billing dashboard
			m.currentView = viewBilling
			m.loading = true
			m.updateTableRows()
			return m, tea.Batch(
				loadBalance(m.client),
				loadInvoices(m.client),
				loadBillingHistory(m.client),
				m.spinner.Tick,
			)
		case "n", "N":
			if m.currentView == viewClusterResources {
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
			} else if m.currentView == viewDroplets {
				m.creating = true
				m.inputIndex = 0
				m.nameInput.Focus()
				m.err = nil
				m.successMsg = ""
				// Reset selection state
				m.selectingRegion = false
				m.selectingSize = false
				m.selectingImage = false
				m.selectedRegionSlug = ""
				m.selectedSizeSlug = ""
				m.selectedImageSlug = ""
				// Load regions, sizes, and images when opening create form
				return m, tea.Batch(
					loadRegions(m.client),
					loadSizes(m.client),
					loadImages(m.client),
				)
			}
		case "r", "R":
			m.loading = true
			if m.currentView == viewDroplets {
				return m, tea.Batch(loadDroplets(m.client), m.spinner.Tick)
			} else if m.currentView == viewClusterResources {
				return m, tea.Batch(loadClusterResources(m.client, m.selectedCluster, m.clusterResourceType, m.selectedNamespace), m.spinner.Tick)
			} else if m.currentView == viewBilling {
				return m, tea.Batch(
					loadBalance(m.client),
					loadInvoices(m.client),
					loadBillingHistory(m.client),
					m.spinner.Tick,
				)
			} else {
				return m, tea.Batch(loadClusters(m.client), m.spinner.Tick)
			}
		case "d", "D":
			// Switch resource types in cluster view, or delete in droplets view
			if m.currentView == viewClusterResources {
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
			} else if m.currentView == viewDroplets {
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
			// SSH into selected droplet - show IP selection menu
			if m.currentView == viewDroplets {
				if m.table.SelectedRow() != nil && len(m.table.SelectedRow()) > 0 {
					selectedName := m.table.SelectedRow()[0]
					for _, d := range m.droplets {
						if d.Name == selectedName {
							// Check if droplet is active
							if d.Status != "active" {
								m.err = fmt.Errorf("droplet %s is not active (status: %s)", d.Name, d.Status)
								return m, nil
							}

							publicIP := getPublicIP(d)
							privateIP := getPrivateIP(d)

							if publicIP == "" && privateIP == "" {
								m.err = fmt.Errorf("droplet %s has no IP address", d.Name)
								return m, nil
							}

							// If both IPs exist, show selection menu
							if publicIP != "" && privateIP != "" {
								m.selectingSSHIP = true
								m.selectedDroplet = &d
								m.sshIPType = "public" // Default to public
								return m, nil
							}

							// If only one IP type exists, use it directly
							var ip string
							if publicIP != "" {
								ip = publicIP
							} else {
								ip = privateIP
							}

							// Start SSH terminal view
							return m.startSSHTerminalView(ip, d.Name)
						}
					}
				}
			}
			return m, nil
		case "enter":
			if m.table.SelectedRow() != nil && len(m.table.SelectedRow()) > 0 {
				selectedName := m.table.SelectedRow()[0]
				if m.currentView == viewDroplets {
					for i := range m.droplets {
						if m.droplets[i].Name == selectedName {
							m.viewingDetails = true
							m.selectedDroplet = &m.droplets[i]
							m.selectedCluster = nil
							break
						}
					}
				} else if m.currentView == viewClusters {
					// Enter cluster - show resource types menu
					for i := range m.clusters {
						if m.clusters[i].Name == selectedName {
							m.currentView = viewClusterResources
							m.selectedCluster = m.clusters[i]
							m.selectedDroplet = nil
							m.clusterResourceType = "deployments" // Default to deployments
							m.selectedNamespace = ""              // Start with all namespaces
							m.loading = true
							m.updateTableRows()
							return m, tea.Batch(loadClusterResources(m.client, m.clusters[i], "deployments", ""), m.spinner.Tick)
						}
					}
				} else if m.currentView == viewClusterResources && m.clusterResourceType == "namespaces" {
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
				} else if m.currentView == viewBilling {
					if m.billingMode == "monthly" && m.selectedBillingMonth == "" {
						// Enter month to see details - convert "Jan 2024" back to "2024-01"
						monthDisplay := selectedName
						// Try to parse the month display format
						if parsedTime, err := time.Parse("Jan 2006", monthDisplay); err == nil {
							m.selectedBillingMonth = parsedTime.Format("2006-01")
							m.updateBillingTable()
						} else if _, err := time.Parse("2006-01", monthDisplay); err == nil {
							// Already in YYYY-MM format
							m.selectedBillingMonth = monthDisplay
							m.updateBillingTable()
						}
					} else if m.billingMode == "monthly" && m.selectedBillingMonth != "" {
						// Enter billing entry to see details
						selectedRow := m.table.SelectedRow()
						if len(selectedRow) >= 5 {
							// Find the entry by date and description
							dateStr := selectedRow[0]
							description := selectedRow[1]
							monthlyData := groupBillingByMonth(m.billingHistory)
							entries := monthlyData[m.selectedBillingMonth]
							for i := range entries {
								if entries[i].Date.Format("2006-01-02") == dateStr && strings.Contains(entries[i].Description, strings.TrimSuffix(description, "...")) {
									m.viewingBillingDetails = true
									m.selectedBillingEntry = &entries[i]
									m.selectedInvoice = nil
									m.billingDetailsScroll = 0 // Reset scroll position
									break
								}
							}
						}
					} else if m.billingMode == "invoices" {
						// Enter invoice to see details
						selectedRow := m.table.SelectedRow()
						if len(selectedRow) >= 1 {
							uuid := selectedRow[0]
							var invoiceUUID string
							// Remove truncation if present
							if strings.HasSuffix(uuid, "...") {
								// Find full UUID
								for i := range m.billingInvoices {
									if strings.HasPrefix(m.billingInvoices[i].InvoiceUUID, uuid[:len(uuid)-3]) {
										m.selectedInvoice = &m.billingInvoices[i]
										invoiceUUID = m.billingInvoices[i].InvoiceUUID
										break
									}
								}
							} else {
								// Find by exact UUID
								for i := range m.billingInvoices {
									if m.billingInvoices[i].InvoiceUUID == uuid {
										m.selectedInvoice = &m.billingInvoices[i]
										invoiceUUID = m.billingInvoices[i].InvoiceUUID
										break
									}
								}
							}
							// Load detailed invoice information
							if invoiceUUID != "" {
								m.loading = true
								m.selectedBillingEntry = nil
								return m, tea.Batch(loadInvoiceDetails(m.client, invoiceUUID), m.spinner.Tick)
							}
						}
					}
				}
			}
			return m, nil
		case "0":
			if m.currentView == viewDroplets {
				m.selectedRegion = "all"
				m.updateTableRows()
			}
			return m, nil
		case "4", "5", "6", "7", "8", "9":
			if m.currentView == viewDroplets {
				idx, _ := strconv.Atoi(msg.String())
				if idx > 0 && idx <= len(m.regions) {
					m.selectedRegion = m.regions[idx-1]
					m.updateTableRows()
				}
			}
			return m, nil
		case "m", "M":
			// Switch to monthly billing view
			if m.currentView == viewBilling {
				m.billingMode = "monthly"
				m.selectedBillingMonth = "" // Reset to monthly summary
				m.updateBillingTable()
			}
			return m, nil
		case "i", "I":
			// Switch to invoices view
			if m.currentView == viewBilling {
				m.billingMode = "invoices"
				m.selectedBillingMonth = "" // Clear month selection
				m.updateBillingTable()
			}
			return m, nil
		case "esc":
			// Go back from cluster resources to clusters list
			if m.currentView == viewClusterResources {
				m.currentView = viewClusters
				m.selectedCluster = nil
				m.updateTableRows()
				return m, nil
			}
			// Go back from month details to monthly summary
			if m.currentView == viewBilling && m.billingMode == "monthly" && m.selectedBillingMonth != "" {
				m.selectedBillingMonth = ""
				m.updateBillingTable()
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

	case regionsLoadedMsg:
		m.availableRegions = msg
		return m, nil

	case sizesLoadedMsg:
		m.availableSizes = msg
		return m, nil

	case imagesLoadedMsg:
		m.availableImages = msg
		return m, nil

	case balanceLoadedMsg:
		m.billingBalance = msg
		m.loading = false
		return m, nil

	case invoicesLoadedMsg:
		m.billingInvoices = msg
		m.loading = false
		// Update billing table after invoices are loaded
		if m.currentView == viewBilling {
			m.updateBillingTable()
		}
		return m, nil

	case billingHistoryLoadedMsg:
		m.billingHistory = msg
		m.loading = false
		// Update billing table after history is loaded
		if m.currentView == viewBilling {
			m.updateBillingTable()
		}
		return m, nil

	case invoiceDetailsLoadedMsg:
		m.detailedInvoice = msg
		m.loading = false
		m.viewingBillingDetails = true
		m.billingDetailsScroll = 0 // Reset scroll position when loading new invoice
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
		// If error occurs during SSH, close terminal
		if m.sshTerminalActive {
			m.closeSSHTerminal()
		}
		return m, nil

	case sshTerminalStartedMsg:
		// SSH terminal started, store PTY and start polling for output immediately
		m.sshTerminalPTY = msg.ptmx
		m.sshTerminalCmd = msg.cmd
		m.sshTerminalHost = msg.name
		m.sshTerminalIP = msg.ip

		// CRITICAL: Set PTY size immediately with current window dimensions
		// ncurses apps need accurate terminal size from the start
		if m.sshTerminalPTY != nil {
			availableRows := m.height - getTopPadding() - 6 // Header + padding + help text
			if availableRows < 5 {
				availableRows = 5
			}
			availableCols := m.width - 4 // Account for border and padding
			if availableCols < 40 {
				availableCols = 40
			}
			pty.Setsize(m.sshTerminalPTY, &pty.Winsize{
				Rows: uint16(availableRows),
				Cols: uint16(availableCols),
			})
			// Also update terminal emulator size
			if m.sshTerminalEmulator != nil {
				m.sshTerminalEmulator.Resize(availableCols, availableRows)
			}
		}

		// Start polling immediately with a fast ticker
		cmds = append(cmds, waitForSSHOutput(m.sshOutputChan))
		return m, tea.Batch(cmds...)

	case sshTerminalOutputMsg:
		// New output from SSH - process through terminal emulator
		// CRITICAL: Use VTE to interpret ANSI escape sequences properly
		// The terminal emulator is the SINGLE SOURCE OF TRUTH for display
		m.sshTerminalMutex.Lock()
		output := string(msg)
		if len(output) > 0 {
			// Process output through terminal emulator to interpret ANSI sequences
			// This handles: cursor positioning, screen clearing (\r, \x1b[K, \x1b[2K), etc.
			// CRITICAL: Process ALL bytes including newlines, carriage returns, etc.
			if m.sshTerminalEmulator != nil && m.sshTerminalParser != nil {
				// Process all bytes through the parser
				// CRITICAL: Convert string back to []byte to preserve ALL control characters
				// This ensures \n, \r, and all ANSI sequences are processed correctly
				outputBytes := []byte(output)

				// Process all bytes through the parser
				// The parser will call methods on the terminal buffer to update the screen
				// This MUST process every byte including \n, \r, and all ANSI sequences
				// The emulator maintains the screen state including cursor position
				//
				// CRITICAL: Newlines (\n) should move cursor to next line in the buffer
				// Carriage returns (\r) should move cursor to start of current line
				// The emulator handles all of this - we just feed it the raw bytes
				m.sshTerminalParser.Advance(m.sshTerminalEmulator, outputBytes)
			}
			// Keep raw output buffer only for debugging/fallback (not used for display)
			if m.sshTerminalRawOutput != nil {
				m.sshTerminalRawOutput.WriteString(output)
				// Limit raw output buffer size (keep last 1MB for debugging)
				rawOutputStr := m.sshTerminalRawOutput.String()
				if len(rawOutputStr) > 1024*1024 { // 1MB
					keepFrom := len(rawOutputStr) - 1024*1024
					m.sshTerminalRawOutput.Reset()
					m.sshTerminalRawOutput.WriteString(rawOutputStr[keepFrom:])
				}
			}
			// The terminal emulator buffer is the SINGLE SOURCE OF TRUTH for rendering
			// All output is processed through the emulator which handles ANSI sequences correctly
		}
		m.sshTerminalMutex.Unlock()
		// Continue waiting for more output
		cmds = append(cmds, waitForSSHOutput(m.sshOutputChan))
		return m, tea.Batch(cmds...)

	case sshTerminalClosedMsg:
		// SSH connection closed
		m.closeSSHTerminal()
		return m, nil

	case time.Time:
		// Ticker message - this is from our SSH output poller
		// If we're in SSH terminal mode, keep the ticker running
		if m.sshTerminalActive && m.sshTerminalPTY != nil {
			// The ticker itself returns time.Time, so this means no message was available
			// Just restart the ticker to keep polling
			cmds = append(cmds, waitForSSHOutput(m.sshOutputChan))
		}
		return m, tea.Batch(cmds...)

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

		// Update PTY size when window resizes (if SSH terminal is active)
		// CRITICAL: ncurses apps need accurate terminal size for proper rendering
		if m.sshTerminalPTY != nil {
			// Calculate available terminal size (accounting for header and padding)
			availableRows := rawHeight - getTopPadding() - 6 // Header + padding + help text
			if availableRows < 5 {
				availableRows = 5 // Minimum size
			}
			availableCols := rawWidth - 4 // Account for border and padding
			if availableCols < 40 {
				availableCols = 40 // Minimum width
			}
			pty.Setsize(m.sshTerminalPTY, &pty.Winsize{
				Rows: uint16(availableRows),
				Cols: uint16(availableCols),
			})
			// Also update terminal emulator size
			if m.sshTerminalEmulator != nil {
				m.sshTerminalEmulator.Resize(availableCols, availableRows)
			}
		}

		// Immediately update all dynamic components
		m.updateAllDimensions(rawWidth, rawHeight)

		// Return commands to trigger a re-render
		return m, tea.Batch(cmds...)
	}

	if !m.loading && !m.creating && !m.viewingDetails && !m.confirmDelete && !m.selectingSSHIP {
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

	// Calculate available height: total height - top padding - top bar - status bar - padding
	// Top padding: dynamic based on terminal (applied globally), status bar: 1 line, padding: 1 line
	tableHeight := height - getTopPadding() - topBarHeight - 2
	if tableHeight < 3 {
		tableHeight = 3
	}
	if tableHeight > 100 {
		tableHeight = 100 // Reasonable maximum
	}

	// Set table dimensions - this must happen before column width calculation
	m.table.SetWidth(tableWidth)
	m.table.SetHeight(tableHeight)

	// Update column widths based on available space (only for droplets view)
	if m.currentView == viewDroplets {
		m.updateColumnWidths(tableWidth)
	}
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
	if m.currentView == viewClusters {
		return
	}

	// Minimum column widths (optimized for small screens)
	// STATUS needs more width to show full status text like "● ACTIVE"
	minWidths := map[string]int{
		"NAME":   8,
		"STATUS": 12, // Increased to show full status like "● ACTIVE" (icon + space + text)
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
			// Reduce from least important columns first
			// Column indices: 0=NAME, 1=STATUS, 2=REGION, 3=SIZE, 4=IP, 5=IMAGE, 6=AGE
			// Priority: Reduce AGE (6), then IMAGE (5), then SIZE (3), protect STATUS (1)
			excess := total - availableWidth
			for excess > 0 {
				reduced := false
				// Try reducing AGE first (least important)
				if excess > 0 && columns[6].Width > minWidths["AGE"] {
					reduce := min(excess, columns[6].Width-minWidths["AGE"])
					columns[6].Width -= reduce
					excess -= reduce
					reduced = true
				}
				// Then reduce IMAGE
				if excess > 0 && columns[5].Width > minWidths["IMAGE"] {
					reduce := min(excess, columns[5].Width-minWidths["IMAGE"])
					columns[5].Width -= reduce
					excess -= reduce
					reduced = true
				}
				// Then reduce SIZE
				if excess > 0 && columns[3].Width > minWidths["SIZE"] {
					reduce := min(excess, columns[3].Width-minWidths["SIZE"])
					columns[3].Width -= reduce
					excess -= reduce
					reduced = true
				}
				// Finally reduce IP if needed
				if excess > 0 && columns[4].Width > minWidths["IP"] {
					reduce := min(excess, columns[4].Width-minWidths["IP"])
					columns[4].Width -= reduce
					excess -= reduce
					reduced = true
				}
				if !reduced {
					break // Can't reduce further without breaking minimums
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

// Helper function to get public IP address from droplet
func getPublicIP(d godo.Droplet) string {
	for _, v4 := range d.Networks.V4 {
		if v4.Type == "public" {
			return v4.IPAddress
		}
	}
	// Fallback to first IP if no public IP found
	if len(d.Networks.V4) > 0 {
		return d.Networks.V4[0].IPAddress
	}
	return ""
}

// Helper function to get private IP address from droplet
func getPrivateIP(d godo.Droplet) string {
	for _, v4 := range d.Networks.V4 {
		if v4.Type == "private" {
			return v4.IPAddress
		}
	}
	return ""
}

// Helper function to get IP address based on type (public or private)
func getIPByType(d godo.Droplet, ipType string) string {
	if ipType == "private" {
		return getPrivateIP(d)
	}
	return getPublicIP(d)
}

func (m *model) updateTableRows() {
	var rows []table.Row

	if m.currentView == viewBilling {
		// Show billing dashboard
		m.updateBillingTable()
		return
	} else if m.currentView == viewClusterResources {
		// Show cluster resources (deployments, pods, etc.)
		m.updateClusterResourceTable()
		return
	} else if m.currentView == viewClusters {
		// Update table columns for clusters - make responsive
		tableWidth := m.width - 2
		if tableWidth < 50 {
			tableWidth = 50
		}
		availableWidth := tableWidth - 6 // Account for borders

		// Calculate responsive widths
		nameWidth := max(int(float64(availableWidth)*0.30), 15)
		statusWidth := max(int(float64(availableWidth)*0.15), 10)
		regionWidth := max(int(float64(availableWidth)*0.12), 8)
		versionWidth := max(int(float64(availableWidth)*0.15), 10)
		nodePoolsWidth := max(int(float64(availableWidth)*0.12), 10)
		nodesWidth := max(int(float64(availableWidth)*0.08), 6)
		ageWidth := max(int(float64(availableWidth)*0.08), 6)

		// Ensure total doesn't exceed available width
		total := nameWidth + statusWidth + regionWidth + versionWidth + nodePoolsWidth + nodesWidth + ageWidth
		if total > availableWidth {
			scale := float64(availableWidth) / float64(total)
			nameWidth = int(float64(nameWidth) * scale)
			statusWidth = int(float64(statusWidth) * scale)
			regionWidth = int(float64(regionWidth) * scale)
			versionWidth = int(float64(versionWidth) * scale)
			nodePoolsWidth = int(float64(nodePoolsWidth) * scale)
			nodesWidth = int(float64(nodesWidth) * scale)
			ageWidth = int(float64(ageWidth) * scale)
		}

		m.table.SetColumns([]table.Column{
			{Title: "NAME", Width: nameWidth},
			{Title: "STATUS", Width: statusWidth},
			{Title: "REGION", Width: regionWidth},
			{Title: "VERSION", Width: versionWidth},
			{Title: "NODE POOLS", Width: nodePoolsWidth},
			{Title: "NODES", Width: nodesWidth},
			{Title: "AGE", Width: ageWidth},
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

			// Truncate status text to fit column
			statusText := strings.ToUpper(status)
			maxStatusTextLen := statusWidth - 2 // Reserve 2 for icon+space
			if maxStatusTextLen < 3 {
				maxStatusTextLen = 3
			}
			if len(statusText) > maxStatusTextLen {
				statusText = statusText[:maxStatusTextLen]
			}

			statusStyle := lipgloss.NewStyle().Foreground(statusColor).Bold(true)
			statusDisplay := statusStyle.Render(fmt.Sprintf("%s %s", statusIcon, statusText))

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

			// Truncate values to fit columns
			clusterName := c.Name
			if len(clusterName) > nameWidth {
				if nameWidth <= 3 {
					clusterName = "..."
				} else {
					clusterName = clusterName[:nameWidth-3] + "..."
				}
			}

			regionSlug := c.RegionSlug
			if len(regionSlug) > regionWidth {
				if regionWidth <= 3 {
					regionSlug = "..."
				} else {
					regionSlug = regionSlug[:regionWidth-3] + "..."
				}
			}

			versionSlug := c.VersionSlug
			if len(versionSlug) > versionWidth {
				if versionWidth <= 3 {
					versionSlug = "..."
				} else {
					versionSlug = versionSlug[:versionWidth-3] + "..."
				}
			}

			rows = append(rows, table.Row{
				clusterName,
				statusDisplay,
				regionSlug,
				versionSlug,
				fmt.Sprintf("%d", nodePoolCount),
				fmt.Sprintf("%d", totalNodes),
				age,
			})
		}
	} else {
		// Update table columns for droplets - initial widths, will be adjusted by updateColumnWidths
		// But ensure STATUS has minimum width to avoid truncation
		m.table.SetColumns([]table.Column{
			{Title: "NAME", Width: 25},
			{Title: "STATUS", Width: 12}, // Ensure minimum for status display
			{Title: "REGION", Width: 10},
			{Title: "SIZE", Width: 15},
			{Title: "IP", Width: 16},
			{Title: "IMAGE", Width: 20},
			{Title: "AGE", Width: 10},
		})

		// Get actual column widths for truncation
		tableColumns := m.table.Columns()
		nameColWidth := 25
		ipColWidth := 13
		imageColWidth := 20
		statusColWidth := 12
		sizeColWidth := 15

		// Extract actual widths from table columns
		for _, col := range tableColumns {
			switch col.Title {
			case "NAME":
				nameColWidth = col.Width
			case "IP":
				ipColWidth = col.Width
			case "IMAGE":
				imageColWidth = col.Width
			case "STATUS":
				statusColWidth = col.Width
			case "SIZE":
				sizeColWidth = col.Width
			}
		}

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
			// Format status text - truncate if too long for column
			statusText := strings.ToUpper(status)
			// Limit status text to fit column (icon + space + text, reserve 2 chars for icon+space)
			maxStatusTextLen := statusColWidth - 2
			if maxStatusTextLen < 3 {
				maxStatusTextLen = 3
			}
			if len(statusText) > maxStatusTextLen {
				statusText = statusText[:maxStatusTextLen]
			}
			statusStyle := lipgloss.NewStyle().Foreground(statusColor).Bold(true)
			statusDisplay := statusStyle.Render(fmt.Sprintf("%s %s", statusIcon, statusText))

			// Always show public IP in table, with private IP if available
			publicIP := getPublicIP(d)
			privateIP := getPrivateIP(d)

			ip := "No IP"
			if publicIP != "" {
				// Show public IP, and private IP if available (format: "public (private)")
				// But truncate to fit column width - prefer showing public IP
				if privateIP != "" {
					// Check if both IPs fit in column (account for " ()" = 4 chars)
					if len(publicIP)+len(privateIP)+4 <= ipColWidth {
						ip = fmt.Sprintf("%s (%s)", publicIP, privateIP)
					} else {
						// Too long, just show public IP
						ip = publicIP
					}
				} else {
					ip = publicIP
				}
			} else if privateIP != "" {
				// Only private IP available
				ip = privateIP
			} else if len(d.Networks.V4) > 0 {
				// Fallback to first IP if no public/private detected
				ip = d.Networks.V4[0].IPAddress
			}

			// Truncate IP to fit column
			if len(ip) > ipColWidth {
				if ipColWidth <= 3 {
					ip = "..."
				} else {
					ip = ip[:ipColWidth-3] + "..."
				}
			}

			// Format size - extract vCPU and memory, truncate if needed
			sizeDisplay := d.SizeSlug
			if strings.Contains(d.SizeSlug, "-") {
				parts := strings.Split(d.SizeSlug, "-")
				if len(parts) >= 3 {
					// Format as "2vCPU 4GB" for better readability
					sizeDisplay = fmt.Sprintf("%svCPU %s", strings.ToUpper(parts[1]), strings.ToUpper(parts[2]))
				}
			}
			// Truncate size if too long
			if len(sizeDisplay) > sizeColWidth {
				if sizeColWidth <= 3 {
					sizeDisplay = "..."
				} else {
					sizeDisplay = sizeDisplay[:sizeColWidth-3] + "..."
				}
			}

			// Format age with better granularity (hours, days, months, years)
			age := "N/A"
			if d.Created != "" {
				if t, err := time.Parse(time.RFC3339, d.Created); err == nil {
					duration := time.Since(t)
					hours := duration.Hours()
					switch {
					case hours < 24:
						age = fmt.Sprintf("%.0fh", hours)
					case hours < 720: // Less than 30 days
						days := hours / 24
						age = fmt.Sprintf("%.0fd", days)
					default:
						// For older droplets, show months or years
						days := hours / 24
						if days >= 365 {
							years := int(days / 365)
							age = fmt.Sprintf("%dy", years)
						} else {
							months := int(days / 30)
							if months > 0 {
								age = fmt.Sprintf("%dmo", months)
							} else {
								age = fmt.Sprintf("%.0fd", days)
							}
						}
					}
				}
			}

			// Truncate long names and image names based on actual column widths
			dropletName := d.Name
			if len(dropletName) > nameColWidth {
				if nameColWidth <= 3 {
					dropletName = "..."
				} else {
					dropletName = dropletName[:nameColWidth-3] + "..."
				}
			}

			imageName := d.Image.Name
			if len(imageName) > imageColWidth {
				if imageColWidth <= 3 {
					imageName = "..."
				} else {
					imageName = imageName[:imageColWidth-3] + "..."
				}
			}

			rows = append(rows, table.Row{
				dropletName,
				statusDisplay,
				d.Region.Slug,
				sizeDisplay,
				ip,
				imageName,
				age,
			})
		}
	}

	m.table.SetRows(rows)
}

// Helper function to parse amount string and return float64
func parseAmount(amountStr string) float64 {
	// Remove $ and commas, then parse
	cleaned := strings.ReplaceAll(strings.ReplaceAll(amountStr, "$", ""), ",", "")
	val, err := strconv.ParseFloat(cleaned, 64)
	if err != nil {
		return 0.0
	}
	return val
}

// Helper function to format amount
func formatAmount(amount float64) string {
	if amount < 0 {
		return fmt.Sprintf("-$%.2f", -amount)
	}
	return fmt.Sprintf("$%.2f", amount)
}

// Group billing history by month
func groupBillingByMonth(history *godo.BillingHistory) map[string][]godo.BillingHistoryEntry {
	monthlyData := make(map[string][]godo.BillingHistoryEntry)

	if history == nil || history.BillingHistory == nil {
		return monthlyData
	}

	for _, entry := range history.BillingHistory {
		monthKey := entry.Date.Format("2006-01") // YYYY-MM format
		monthlyData[monthKey] = append(monthlyData[monthKey], entry)
	}

	return monthlyData
}

// Calculate total for a month
func calculateMonthTotal(entries []godo.BillingHistoryEntry) float64 {
	total := 0.0
	for _, entry := range entries {
		total += parseAmount(entry.Amount)
	}
	return total
}

func (m *model) updateBillingTable() {
	// Update table columns for billing
	tableWidth := m.width - 2
	if tableWidth < 50 {
		tableWidth = 50
	}
	availableWidth := tableWidth - 6

	// CRITICAL: Clear rows FIRST before setting columns
	m.table.SetRows([]table.Row{})

	var columns []table.Column
	var rows []table.Row

	if m.billingMode == "monthly" {
		// Monthly summary view
		if m.selectedBillingMonth == "" {
			// Show monthly summary
			columns = []table.Column{
				{Title: "MONTH", Width: max(int(float64(availableWidth)*0.30), 15)},
				{Title: "TOTAL", Width: max(int(float64(availableWidth)*0.25), 12)},
				{Title: "ENTRIES", Width: max(int(float64(availableWidth)*0.20), 10)},
				{Title: "LAST ENTRY", Width: max(int(float64(availableWidth)*0.25), 15)},
			}
			m.table.SetColumns(columns)

			monthlyData := groupBillingByMonth(m.billingHistory)

			// Sort months (most recent first)
			type monthInfo struct {
				month    string
				total    float64
				count    int
				lastDate time.Time
			}
			var months []monthInfo
			for month, entries := range monthlyData {
				total := calculateMonthTotal(entries)
				lastDate := time.Time{}
				for _, entry := range entries {
					if entry.Date.After(lastDate) {
						lastDate = entry.Date
					}
				}
				months = append(months, monthInfo{
					month:    month,
					total:    total,
					count:    len(entries),
					lastDate: lastDate,
				})
			}

			// Sort by month (descending - most recent first)
			for i := 0; i < len(months)-1; i++ {
				for j := i + 1; j < len(months); j++ {
					if months[i].month < months[j].month {
						months[i], months[j] = months[j], months[i]
					}
				}
			}

			for _, info := range months {
				monthDisplay := info.month
				// Format as "YYYY-MM" -> "Jan 2024"
				if t, err := time.Parse("2006-01", info.month); err == nil {
					monthDisplay = t.Format("Jan 2006")
				}

				totalStr := formatAmount(info.total)
				if len(totalStr) > 15 {
					totalStr = totalStr[:12] + "..."
				}

				lastDateStr := "N/A"
				if !info.lastDate.IsZero() {
					lastDateStr = info.lastDate.Format("2006-01-02")
				}

				rows = append(rows, table.Row{
					monthDisplay,
					totalStr,
					fmt.Sprintf("%d", info.count),
					lastDateStr,
				})
			}
		} else {
			// Show details for selected month
			columns = []table.Column{
				{Title: "DATE", Width: max(int(float64(availableWidth)*0.15), 12)},
				{Title: "DESCRIPTION", Width: max(int(float64(availableWidth)*0.35), 20)},
				{Title: "AMOUNT", Width: max(int(float64(availableWidth)*0.15), 12)},
				{Title: "TYPE", Width: max(int(float64(availableWidth)*0.15), 12)},
				{Title: "INVOICE UUID", Width: max(int(float64(availableWidth)*0.20), 15)},
			}
			m.table.SetColumns(columns)

			monthlyData := groupBillingByMonth(m.billingHistory)
			entries := monthlyData[m.selectedBillingMonth]

			// Sort entries by date (most recent first)
			for i := 0; i < len(entries)-1; i++ {
				for j := i + 1; j < len(entries); j++ {
					if entries[i].Date.Before(entries[j].Date) {
						entries[i], entries[j] = entries[j], entries[i]
					}
				}
			}

			for _, entry := range entries {
				dateStr := entry.Date.Format("2006-01-02")

				description := entry.Description
				if len(description) > 25 {
					description = description[:22] + "..."
				}

				amount := entry.Amount
				if len(amount) > 12 {
					amount = amount[:9] + "..."
				}

				entryType := entry.Type
				if len(entryType) > 12 {
					entryType = entryType[:9] + "..."
				}

				invoiceUUID := "N/A"
				if entry.InvoiceUUID != nil && *entry.InvoiceUUID != "" {
					uuid := *entry.InvoiceUUID
					if len(uuid) > 18 {
						uuid = uuid[:15] + "..."
					}
					invoiceUUID = uuid
				}

				rows = append(rows, table.Row{
					dateStr,
					description,
					amount,
					entryType,
					invoiceUUID,
				})
			}
		}
	} else {
		// Invoices view (default)
		columns = []table.Column{
			{Title: "INVOICE UUID", Width: max(int(float64(availableWidth)*0.35), 20)},
			{Title: "AMOUNT", Width: max(int(float64(availableWidth)*0.15), 12)},
			{Title: "INVOICE PERIOD", Width: max(int(float64(availableWidth)*0.25), 15)},
			{Title: "DATE", Width: max(int(float64(availableWidth)*0.25), 15)},
		}
		m.table.SetColumns(columns)

		for _, inv := range m.billingInvoices {
			amount := inv.Amount
			if len(amount) > 12 {
				amount = amount[:9] + "..."
			}

			period := inv.InvoicePeriod
			if len(period) > 15 {
				period = period[:12] + "..."
			}

			date := "N/A"
			if !inv.UpdatedAt.IsZero() {
				date = inv.UpdatedAt.Format("2006-01-02")
			}

			uuid := inv.InvoiceUUID
			if len(uuid) > 20 {
				uuid = uuid[:17] + "..."
			}

			rows = append(rows, table.Row{
				uuid,
				amount,
				period,
				date,
			})
		}
	}

	if len(rows) == 0 {
		placeholderRow := make(table.Row, len(columns))
		for i := range placeholderRow {
			placeholderRow[i] = "No data"
		}
		rows = []table.Row{placeholderRow}
	}

	// Now set the rows with the correct column structure
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
	// Update table columns based on resource type - make them responsive
	var columns []table.Column
	var rows []table.Row

	// Calculate available width for table (account for borders)
	tableWidth := m.width - 2
	if tableWidth < 50 {
		tableWidth = 50
	}
	availableWidth := tableWidth - 6 // Account for table borders

	// Helper to truncate values based on column width
	truncateValue := func(value string, maxWidth int) string {
		if len(value) <= maxWidth {
			return value
		}
		if maxWidth <= 3 {
			return "..."
		}
		return value[:maxWidth-3] + "..."
	}

	switch m.clusterResourceType {
	case "deployments":
		// Calculate responsive widths
		nameWidth := max(int(float64(availableWidth)*0.40), 15)
		readyWidth := max(int(float64(availableWidth)*0.15), 8)
		uptodateWidth := max(int(float64(availableWidth)*0.15), 10)
		availableWidthCol := max(int(float64(availableWidth)*0.15), 10)
		ageWidth := max(int(float64(availableWidth)*0.15), 6)

		// Ensure total doesn't exceed available width
		total := nameWidth + readyWidth + uptodateWidth + availableWidthCol + ageWidth
		if total > availableWidth {
			scale := float64(availableWidth) / float64(total)
			nameWidth = int(float64(nameWidth) * scale)
			readyWidth = int(float64(readyWidth) * scale)
			uptodateWidth = int(float64(uptodateWidth) * scale)
			availableWidthCol = int(float64(availableWidthCol) * scale)
			ageWidth = int(float64(ageWidth) * scale)
		}

		columns = []table.Column{
			{Title: "NAME", Width: nameWidth},
			{Title: "READY", Width: readyWidth},
			{Title: "UP-TO-DATE", Width: uptodateWidth},
			{Title: "AVAILABLE", Width: availableWidthCol},
			{Title: "AGE", Width: ageWidth},
		}
		// Use actual resources from cluster
		for _, r := range m.clusterResources {
			name := truncateValue(getMapValue(r, "name", "N/A"), nameWidth)
			rows = append(rows, table.Row{
				name,
				getMapValue(r, "ready", "0/0"),
				getMapValue(r, "upToDate", "0"),
				getMapValue(r, "available", "0"),
				getMapValue(r, "age", "N/A"),
			})
		}
	case "pods":
		nameWidth := max(int(float64(availableWidth)*0.45), 15)
		readyWidth := max(int(float64(availableWidth)*0.15), 8)
		statusWidth := max(int(float64(availableWidth)*0.15), 10)
		restartsWidth := max(int(float64(availableWidth)*0.10), 8)
		ageWidth := max(int(float64(availableWidth)*0.15), 6)

		total := nameWidth + readyWidth + statusWidth + restartsWidth + ageWidth
		if total > availableWidth {
			scale := float64(availableWidth) / float64(total)
			nameWidth = int(float64(nameWidth) * scale)
			readyWidth = int(float64(readyWidth) * scale)
			statusWidth = int(float64(statusWidth) * scale)
			restartsWidth = int(float64(restartsWidth) * scale)
			ageWidth = int(float64(ageWidth) * scale)
		}

		columns = []table.Column{
			{Title: "NAME", Width: nameWidth},
			{Title: "READY", Width: readyWidth},
			{Title: "STATUS", Width: statusWidth},
			{Title: "RESTARTS", Width: restartsWidth},
			{Title: "AGE", Width: ageWidth},
		}
		for _, r := range m.clusterResources {
			name := truncateValue(getMapValue(r, "name", "N/A"), nameWidth)
			rows = append(rows, table.Row{
				name,
				getMapValue(r, "ready", "0/0"),
				truncateValue(getMapValue(r, "status", "Unknown"), statusWidth),
				getMapValue(r, "restarts", "0"),
				getMapValue(r, "age", "N/A"),
			})
		}
	case "services":
		nameWidth := max(int(float64(availableWidth)*0.35), 15)
		typeWidth := max(int(float64(availableWidth)*0.15), 10)
		clusterIPWidth := max(int(float64(availableWidth)*0.20), 12)
		externalIPWidth := max(int(float64(availableWidth)*0.20), 12)
		ageWidth := max(int(float64(availableWidth)*0.10), 6)

		total := nameWidth + typeWidth + clusterIPWidth + externalIPWidth + ageWidth
		if total > availableWidth {
			scale := float64(availableWidth) / float64(total)
			nameWidth = int(float64(nameWidth) * scale)
			typeWidth = int(float64(typeWidth) * scale)
			clusterIPWidth = int(float64(clusterIPWidth) * scale)
			externalIPWidth = int(float64(externalIPWidth) * scale)
			ageWidth = int(float64(ageWidth) * scale)
		}

		columns = []table.Column{
			{Title: "NAME", Width: nameWidth},
			{Title: "TYPE", Width: typeWidth},
			{Title: "CLUSTER-IP", Width: clusterIPWidth},
			{Title: "EXTERNAL-IP", Width: externalIPWidth},
			{Title: "AGE", Width: ageWidth},
		}
		for _, r := range m.clusterResources {
			name := truncateValue(getMapValue(r, "name", "N/A"), nameWidth)
			clusterIP := truncateValue(getMapValue(r, "clusterIP", "<none>"), clusterIPWidth)
			externalIP := truncateValue(getMapValue(r, "externalIP", "<none>"), externalIPWidth)
			rows = append(rows, table.Row{
				name,
				truncateValue(getMapValue(r, "type", "ClusterIP"), typeWidth),
				clusterIP,
				externalIP,
				getMapValue(r, "age", "N/A"),
			})
		}
	case "nodes":
		nameWidth := max(int(float64(availableWidth)*0.40), 15)
		statusWidth := max(int(float64(availableWidth)*0.20), 10)
		rolesWidth := max(int(float64(availableWidth)*0.15), 8)
		ageWidth := max(int(float64(availableWidth)*0.10), 6)
		versionWidth := max(int(float64(availableWidth)*0.15), 10)

		total := nameWidth + statusWidth + rolesWidth + ageWidth + versionWidth
		if total > availableWidth {
			scale := float64(availableWidth) / float64(total)
			nameWidth = int(float64(nameWidth) * scale)
			statusWidth = int(float64(statusWidth) * scale)
			rolesWidth = int(float64(rolesWidth) * scale)
			ageWidth = int(float64(ageWidth) * scale)
			versionWidth = int(float64(versionWidth) * scale)
		}

		columns = []table.Column{
			{Title: "NAME", Width: nameWidth},
			{Title: "STATUS", Width: statusWidth},
			{Title: "ROLES", Width: rolesWidth},
			{Title: "AGE", Width: ageWidth},
			{Title: "VERSION", Width: versionWidth},
		}
		for _, r := range m.clusterResources {
			name := truncateValue(getMapValue(r, "name", "N/A"), nameWidth)
			version := truncateValue(getMapValue(r, "version", "N/A"), versionWidth)
			rows = append(rows, table.Row{
				name,
				truncateValue(getMapValue(r, "status", "Unknown"), statusWidth),
				truncateValue(getMapValue(r, "roles", "<none>"), rolesWidth),
				getMapValue(r, "age", "N/A"),
				version,
			})
		}
	case "namespaces":
		nameWidth := max(int(float64(availableWidth)*0.60), 20)
		statusWidth := max(int(float64(availableWidth)*0.20), 10)
		ageWidth := max(int(float64(availableWidth)*0.20), 8)

		total := nameWidth + statusWidth + ageWidth
		if total > availableWidth {
			scale := float64(availableWidth) / float64(total)
			nameWidth = int(float64(nameWidth) * scale)
			statusWidth = int(float64(statusWidth) * scale)
			ageWidth = int(float64(ageWidth) * scale)
		}

		columns = []table.Column{
			{Title: "NAME", Width: nameWidth},
			{Title: "STATUS", Width: statusWidth},
			{Title: "AGE", Width: ageWidth},
		}
		// Add "all" option at the top for selecting all namespaces
		rows = append(rows, table.Row{"all", "Active", "N/A"})
		for _, r := range m.clusterResources {
			name := truncateValue(getMapValue(r, "name", "N/A"), nameWidth)
			rows = append(rows, table.Row{
				name,
				truncateValue(getMapValue(r, "status", "Unknown"), statusWidth),
				getMapValue(r, "age", "N/A"),
			})
		}
	default:
		nameWidth := max(int(float64(availableWidth)*0.60), 20)
		statusWidth := max(int(float64(availableWidth)*0.20), 10)
		ageWidth := max(int(float64(availableWidth)*0.20), 8)

		columns = []table.Column{
			{Title: "NAME", Width: nameWidth},
			{Title: "STATUS", Width: statusWidth},
			{Title: "AGE", Width: ageWidth},
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

func (m model) updateSSHIPSelection(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "1", "p", "P":
		// Select public IP
		if m.selectedDroplet != nil {
			publicIP := getPublicIP(*m.selectedDroplet)
			if publicIP != "" {
				m.selectingSSHIP = false
				dropletName := m.selectedDroplet.Name
				m.selectedDroplet = nil
				return m.startSSHTerminalView(publicIP, dropletName)
			}
		}
		return m, nil
	case "2", "r", "R":
		// Select private IP
		if m.selectedDroplet != nil {
			privateIP := getPrivateIP(*m.selectedDroplet)
			if privateIP != "" {
				m.selectingSSHIP = false
				dropletName := m.selectedDroplet.Name
				m.selectedDroplet = nil
				return m.startSSHTerminalView(privateIP, dropletName)
			}
		}
		return m, nil
	case "esc", "q", "Q":
		// Cancel SSH selection
		m.selectingSSHIP = false
		m.selectedDroplet = nil
		return m, nil
	}
	return m, nil
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

	// Handle selection mode (when selecting region, size, or image)
	if m.selectingRegion || m.selectingSize || m.selectingImage {
		return m.updateSelectionMode(msg)
	}

	switch msg.String() {
	case "esc":
		m.creating = false
		m.resetInputs()
		m.err = nil
		return m, nil
	case "tab":
		m.inputIndex = (m.inputIndex + 1) % 5 // 5 fields: name(0), region(1), size(2), image(3), tags(4)
		m.updateInputFocus()
		return m, nil
	case "shift+tab":
		m.inputIndex = (m.inputIndex - 1 + 5) % 5
		m.updateInputFocus()
		return m, nil
	case "enter":
		// Open selection for region/size/image, or create droplet if on tags field
		if m.inputIndex == 1 {
			// Region field - open region selection
			m.selectingRegion = true
			m.setupSelectionTable("region")
			return m, nil
		} else if m.inputIndex == 2 {
			// Size field - open size selection
			m.selectingSize = true
			m.setupSelectionTable("size")
			return m, nil
		} else if m.inputIndex == 3 {
			// Image field - open image selection
			m.selectingImage = true
			m.setupSelectionTable("image")
			return m, nil
		} else if m.inputIndex == 4 {
			// Tags field - create droplet
			if m.selectedRegionSlug != "" && m.selectedSizeSlug != "" && m.selectedImageSlug != "" {
				m.loading = true
				return m, tea.Batch(createDroplet(m.client, m), m.spinner.Tick)
			} else {
				m.err = fmt.Errorf("please select region, size, and image")
				return m, nil
			}
		}
		return m, nil
	}

	switch m.inputIndex {
	case 0:
		m.nameInput, cmd = m.nameInput.Update(msg)
	case 4:
		m.tagsInput, cmd = m.tagsInput.Update(msg)
	}

	return m, cmd
}

func (m model) updateSelectionMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg.String() {
	case "esc":
		// Cancel selection
		m.selectingRegion = false
		m.selectingSize = false
		m.selectingImage = false
		return m, nil
	case "enter":
		// Confirm selection
		if m.selectionTable.SelectedRow() != nil && len(m.selectionTable.SelectedRow()) > 0 {
			var selectedSlug string
			if m.selectingRegion {
				// Region table: SLUG is in column 0
				selectedSlug = m.selectionTable.SelectedRow()[0]
				m.selectedRegionSlug = selectedSlug
				m.selectingRegion = false
				m.inputIndex = 2 // Move to size field
			} else if m.selectingSize {
				// Size table: SLUG is in column 0
				selectedSlug = m.selectionTable.SelectedRow()[0]
				m.selectedSizeSlug = selectedSlug
				m.selectingSize = false
				m.inputIndex = 3 // Move to image field
			} else if m.selectingImage {
				// Image table: SLUG is in column 2 (DISTRIBUTION, ARCHITECTURE, SLUG)
				if len(m.selectionTable.SelectedRow()) >= 3 {
					selectedSlug = m.selectionTable.SelectedRow()[2]
					// Remove truncation if present (e.g., "ubuntu-22-04-x64..." -> "ubuntu-22-04-x64")
					selectedSlug = strings.TrimSuffix(selectedSlug, "...")
					m.selectedImageSlug = selectedSlug
				} else {
					// Fallback: try to get from first column if structure is different
					selectedSlug = m.selectionTable.SelectedRow()[0]
					m.selectedImageSlug = selectedSlug
				}
				m.selectingImage = false
				m.inputIndex = 4 // Move to tags field
			}
		}
		return m, nil
	}

	// Handle table navigation
	m.selectionTable, cmd = m.selectionTable.Update(msg)
	return m, cmd
}

func (m *model) setupSelectionTable(selectionType string) {
	var columns []table.Column
	var rows []table.Row

	switch selectionType {
	case "region":
		columns = []table.Column{
			{Title: "SLUG", Width: 15},
			{Title: "NAME", Width: 30},
			{Title: "AVAILABLE", Width: 12},
		}
		for _, r := range m.availableRegions {
			available := "Yes"
			if !r.Available {
				available = "No"
			}
			rows = append(rows, table.Row{
				r.Slug,
				r.Name,
				available,
			})
		}
	case "size":
		columns = []table.Column{
			{Title: "SLUG", Width: 20},
			{Title: "VCPU", Width: 8},
			{Title: "RAM", Width: 12},
			{Title: "DISK", Width: 12},
			{Title: "PRICE", Width: 12},
		}
		for _, s := range m.availableSizes {
			ramGB := float64(s.Memory) / 1024.0
			price := "$0.00"
			if s.PriceMonthly > 0 {
				price = fmt.Sprintf("$%.2f", s.PriceMonthly)
			}
			rows = append(rows, table.Row{
				s.Slug,
				fmt.Sprintf("%d", s.Vcpus),
				fmt.Sprintf("%.0fGB", ramGB),
				fmt.Sprintf("%dGB", s.Disk),
				price,
			})
		}
	case "image":
		columns = []table.Column{
			{Title: "DISTRIBUTION", Width: 20},
			{Title: "ARCHITECTURE", Width: 15},
			{Title: "SLUG", Width: 35},
		}
		for _, img := range m.availableImages {
			// Determine architecture from slug
			slugLower := strings.ToLower(img.Slug)
			architecture := "x64"
			if strings.Contains(slugLower, "-x86") || strings.Contains(slugLower, "_x86") {
				architecture = "x86"
			} else if strings.Contains(slugLower, "-x64") || strings.Contains(slugLower, "_x64") ||
				strings.Contains(slugLower, "-amd64") || strings.Contains(slugLower, "_amd64") {
				architecture = "x64"
			} else {
				// Fallback: check name
				nameLower := strings.ToLower(img.Name)
				if strings.Contains(nameLower, "x86") {
					architecture = "x86"
				} else {
					architecture = "x64" // Default to x64
				}
			}

			// Get distribution
			distribution := img.Distribution
			if distribution == "" {
				// Try to extract from slug (e.g., "ubuntu-22-04-x64" -> "Ubuntu", "debian-12-x64" -> "Debian")
				slugParts := strings.Split(img.Slug, "-")
				if len(slugParts) > 0 {
					distName := slugParts[0]
					// Capitalize properly (debian -> Debian, ubuntu -> Ubuntu)
					if len(distName) > 0 {
						distribution = strings.ToUpper(distName[:1]) + strings.ToLower(distName[1:])
					} else {
						distribution = "Unknown"
					}
				} else {
					distribution = "Unknown"
				}
			} else {
				// Ensure proper capitalization (e.g., "Debian", "Ubuntu")
				distLower := strings.ToLower(distribution)
				if len(distLower) > 0 {
					distribution = strings.ToUpper(distLower[:1]) + distLower[1:]
				}
			}

			// Don't truncate slug - we need the full slug for API calls
			// The table will handle display truncation
			rows = append(rows, table.Row{
				distribution,
				architecture,
				img.Slug, // Use full slug, not truncated
			})
		}
	}

	// CRITICAL: Clear rows FIRST before setting columns
	// This prevents the table library from trying to render old rows with new column structure
	m.selectionTable.SetRows([]table.Row{})

	// Set columns
	m.selectionTable.SetColumns(columns)

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
	m.selectionTable.SetRows(rows)
	m.selectionTable.SetHeight(10)
	m.selectionTable.SetWidth(m.width - 4)
}

func (m *model) updateInputFocus() {
	m.nameInput.Blur()
	m.tagsInput.Blur()

	switch m.inputIndex {
	case 0:
		m.nameInput.Focus()
	case 4:
		m.tagsInput.Focus()
	}
}

func (m *model) resetInputs() {
	m.nameInput.SetValue("")
	m.tagsInput.SetValue("")
	m.inputIndex = 0
	m.selectedRegionSlug = ""
	m.selectedSizeSlug = ""
	m.selectedImageSlug = ""
	m.selectingRegion = false
	m.selectingSize = false
	m.selectingImage = false
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
			"deployments":  true,
			"pods":         true,
			"services":     true,
			"daemonsets":   true,
			"statefulsets": true,
			"pvc":          true,
			"configmaps":   true,
			"secrets":      true,
			"nodes":        true,
			"namespaces":   true,
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

	// Show available resources - truncate if too long
	availableResources := []string{"deployments", "pods", "services", "daemonsets", "statefulsets", "pvc", "configmaps", "secrets", "nodes", "namespaces"}
	helpText := fmt.Sprintf("Available: %s", strings.Join(availableResources, ", "))
	maxHelpLen := m.width - 4
	if len(helpText) > maxHelpLen {
		// Truncate help text to fit
		helpText = helpText[:maxHelpLen-3] + "..."
	}
	helpTextStyled := lipgloss.NewStyle().
		Foreground(mutedColor).
		Render(helpText)

	s.WriteString("\n")
	s.WriteString(helpTextStyled)

	return s.String()
}

func (m model) View() string {
	var content string

	// Get the content from the appropriate render function
	if m.sshTerminalActive {
		content = m.renderSSHTerminal()
	} else if m.commandMode {
		content = m.renderCommandMode()
	} else if m.selectingSSHIP {
		content = m.renderSSHIPSelection()
	} else if m.confirmDelete {
		content = m.renderDeleteConfirmation()
	} else if m.creating {
		content = m.renderCreateForm()
	} else if m.viewingBillingDetails {
		content = m.renderBillingDetails()
	} else if m.viewingDetails {
		if m.selectedDroplet != nil {
			content = m.renderDropletDetails()
		} else if m.selectedCluster != nil && m.currentView != viewClusterResources {
			content = m.renderClusterDetails()
		} else {
			content = m.renderMainView()
		}
	} else {
		content = m.renderMainView()
	}

	// Apply global top padding at the root level
	// This ensures consistent padding across all screens and prevents row 0 rendering
	topPad := getTopPadding()
	return strings.Repeat("\n", topPad) + content
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
	// Note: Top padding is applied globally in View(), not here
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
	if m.currentView == viewClusterResources && m.selectedCluster != nil {
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
	} else if m.currentView == viewClusters {
		accountInfo.WriteString(labelStyle.Render("View: ") + valueStyle.Render("Kubernetes Clusters"))
		accountInfo.WriteString("\n")
		accountInfo.WriteString(labelStyle.Render("Clusters: ") + valueStyle.Render(fmt.Sprintf("%d", m.clusterCount)))
	} else {
		accountInfo.WriteString(labelStyle.Render("Droplets: ") + valueStyle.Render(fmt.Sprintf("%d", m.dropletCount)))
		accountInfo.WriteString("\n")
		// Truncate region if too long to fit panel
		region := m.selectedRegion
		maxRegionLen := panelWidth - 9 // Reserve space for "Region: " label
		if maxRegionLen < 5 {
			maxRegionLen = 5
		}
		if len(region) > maxRegionLen {
			region = truncateString(region, maxRegionLen)
		}
		accountInfo.WriteString(labelStyle.Render("Region: ") + valueStyle.Render(region))
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
		if leftWidth+middleWidth+rightWidth > availableWidth {
			// Reduce left panel if needed, but keep middle panel at minimum
			leftWidth = availableWidth - middleWidth - rightWidth
			if leftWidth < 40 {
				leftWidth = 40
				// If still too wide, reduce right panel
				if leftWidth+middleWidth+rightWidth > availableWidth {
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

	if m.currentView == viewClusterResources {
		// Show cluster info when viewing cluster resources
		if m.selectedCluster != nil {
			// Truncate cluster name
			clusterName := truncateString(m.selectedCluster.Name, leftWidth-10)
			leftContent.WriteString(labelStyle.Render("Cluster: ") + valueStyle.Render(clusterName))
			leftContent.WriteString("\n")
			// Truncate region
			region := truncateString(m.selectedCluster.RegionSlug, leftWidth-10)
			leftContent.WriteString(labelStyle.Render("Region: ") + valueStyle.Render(region))
			leftContent.WriteString("\n")
			// Truncate version
			version := truncateString(m.selectedCluster.VersionSlug, leftWidth-11)
			leftContent.WriteString(labelStyle.Render("Version: ") + valueStyle.Render(version))
			leftContent.WriteString("\n")

			// Show namespace filter
			namespaceDisplay := "all"
			if m.selectedNamespace != "" {
				namespaceDisplay = truncateString(m.selectedNamespace, leftWidth-13)
			}
			leftContent.WriteString(labelStyle.Render("Namespace: ") + valueStyle.Render(namespaceDisplay))
			leftContent.WriteString("\n")

			// Show resource type
			resourceType := truncateString(strings.Title(m.clusterResourceType), leftWidth-11)
			leftContent.WriteString(labelStyle.Render("Resource: ") + valueStyle.Render(resourceType))
			leftContent.WriteString("\n")

			// Show resource count
			leftContent.WriteString(labelStyle.Render("Count: ") + valueStyle.Render(fmt.Sprintf("%d", len(m.clusterResources))))
			leftContent.WriteString("\n")
		}
	} else if m.currentView == viewClusters {
		leftContent.WriteString(labelStyle.Render("View: ") + valueStyle.Render("Kubernetes Clusters"))
		leftContent.WriteString("\n")
		leftContent.WriteString(labelStyle.Render("Clusters: ") + valueStyle.Render(fmt.Sprintf("%d", m.clusterCount)))
	} else if m.currentView == viewBilling {
		modeDisplay := "Invoices"
		if m.billingMode == "monthly" {
			modeDisplay = "Monthly"
			if m.selectedBillingMonth != "" {
				// Format selected month for display
				if t, err := time.Parse("2006-01", m.selectedBillingMonth); err == nil {
					modeDisplay = t.Format("Jan 2006")
				} else {
					modeDisplay = m.selectedBillingMonth
				}
			}
		}
		leftContent.WriteString(labelStyle.Render("View: ") + valueStyle.Render("Billing - "+modeDisplay))
		leftContent.WriteString("\n")
		if m.billingBalance != nil {
			monthToDate := m.billingBalance.MonthToDateBalance
			if len(monthToDate) > leftWidth-18 {
				monthToDate = truncateString(monthToDate, leftWidth-18)
			}
			leftContent.WriteString(labelStyle.Render("Month-to-Date: ") + valueStyle.Render(monthToDate))
			leftContent.WriteString("\n")
			accountBalance := m.billingBalance.AccountBalance
			if len(accountBalance) > leftWidth-18 {
				accountBalance = truncateString(accountBalance, leftWidth-18)
			}
			leftContent.WriteString(labelStyle.Render("Account Balance: ") + valueStyle.Render(accountBalance))
			leftContent.WriteString("\n")
		}
		if m.billingMode == "invoices" {
			leftContent.WriteString(labelStyle.Render("Invoices: ") + valueStyle.Render(fmt.Sprintf("%d", len(m.billingInvoices))))
		} else {
			historyCount := 0
			if m.billingHistory != nil && m.billingHistory.BillingHistory != nil {
				historyCount = len(m.billingHistory.BillingHistory)
			}
			leftContent.WriteString(labelStyle.Render("History Entries: ") + valueStyle.Render(fmt.Sprintf("%d", historyCount)))
		}
	} else {
		// Truncate region if needed
		region := truncateString(m.selectedRegion, leftWidth-9)
		leftContent.WriteString(labelStyle.Render("Region: ") + valueStyle.Render(region))
		leftContent.WriteString("\n")
		leftContent.WriteString(labelStyle.Render("Droplets: ") + valueStyle.Render(fmt.Sprintf("%d", m.dropletCount)))
	}
	leftContent.WriteString("\n")

	if m.account != nil && m.currentView != viewClusterResources {
		// Truncate email to fit panel width
		email := m.account.Email
		maxEmailLen := leftWidth - 10 // Reserve space for "Account: " label
		if maxEmailLen < 5 {
			maxEmailLen = 5
		}
		if len(email) > maxEmailLen {
			email = truncateString(email, maxEmailLen)
		}
		leftContent.WriteString(labelStyle.Render("Account: ") + valueStyle.Render(email))
		leftContent.WriteString("\n")
		// Truncate status if needed
		status := m.account.Status
		maxStatusLen := leftWidth - 9 // Reserve space for "Status: " label
		if maxStatusLen < 5 {
			maxStatusLen = 5
		}
		if len(status) > maxStatusLen {
			status = truncateString(status, maxStatusLen)
		}
		leftContent.WriteString(labelStyle.Render("Status: ") + valueStyle.Render(status))
		leftContent.WriteString("\n")
	}

	refreshTime := "N/A"
	if !m.lastRefresh.IsZero() {
		refreshTime = m.lastRefresh.Format("15:04:05")
	}
	leftContent.WriteString(labelStyle.Render("Refresh: ") + valueStyle.Render(refreshTime))
	leftContent.WriteString("\n")
	leftContent.WriteString(labelStyle.Render("Version: ") + valueStyle.Render("dogoctl v1.4.1"))

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
	if m.currentView == viewBilling {
		middleContent.WriteString(keyStyle.Render("1") + " Droplets\n")
		middleContent.WriteString(keyStyle.Render("2") + " Clusters\n")
		middleContent.WriteString(keyStyle.Render("3") + " Billing\n")
		middleContent.WriteString(keyStyle.Render("m") + " Monthly\n")
		middleContent.WriteString(keyStyle.Render("i") + " Invoices\n")
		middleContent.WriteString(keyStyle.Render("r") + " Refresh\n")
		if m.billingMode == "monthly" {
			if m.selectedBillingMonth == "" {
				middleContent.WriteString(keyStyle.Render("enter") + " Month Details\n")
			} else {
				middleContent.WriteString(keyStyle.Render("esc") + " Back\n")
			}
		}
		middleContent.WriteString(keyStyle.Render("q") + " Quit")
	} else if m.currentView == viewClusterResources {
		middleContent.WriteString(keyStyle.Render("1") + " Droplets\n")
		middleContent.WriteString(keyStyle.Render("2") + " Clusters\n")
		middleContent.WriteString(keyStyle.Render(":") + " Command\n")
		middleContent.WriteString(keyStyle.Render("d") + " Next\n")
		middleContent.WriteString(keyStyle.Render("n") + " Namespace\n")
		middleContent.WriteString(keyStyle.Render("r") + " Refresh\n")
		middleContent.WriteString(keyStyle.Render("enter") + " Details\n")
		middleContent.WriteString(keyStyle.Render("esc") + " Back\n")
		middleContent.WriteString(keyStyle.Render("q") + " Quit")
	} else if m.currentView == viewDroplets {
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
	} else if m.currentView == viewClusters {
		// Clusters view
		middleContent.WriteString(keyStyle.Render("1") + " Droplets\n")
		middleContent.WriteString(keyStyle.Render("2") + " Clusters\n")
		middleContent.WriteString(keyStyle.Render("3") + " Billing\n")
		middleContent.WriteString(keyStyle.Render("r") + " Refresh\n")
		middleContent.WriteString(keyStyle.Render("enter") + " Enter\n")
		middleContent.WriteString(keyStyle.Render("?") + " Help\n")
		middleContent.WriteString(keyStyle.Render("q") + " Quit")
	} else {
		// Billing view (fallback)
		middleContent.WriteString(keyStyle.Render("1") + " Droplets\n")
		middleContent.WriteString(keyStyle.Render("2") + " Clusters\n")
		middleContent.WriteString(keyStyle.Render("3") + " Billing\n")
		middleContent.WriteString(keyStyle.Render("r") + " Refresh\n")
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

	if m.currentView == viewClusterResources && m.selectedCluster != nil {
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
	} else if m.currentView == viewClusters {
		leftContent.WriteString(labelStyle.Render("View: ") + valueStyle.Render("Kubernetes Clusters"))
		leftContent.WriteString("\n")
		leftContent.WriteString(labelStyle.Render("Clusters: ") + valueStyle.Render(fmt.Sprintf("%d", m.clusterCount)))
	} else if m.currentView == viewBilling {
		modeDisplay := "Invoices"
		if m.billingMode == "monthly" {
			modeDisplay = "Monthly"
			if m.selectedBillingMonth != "" {
				// Format selected month for display
				if t, err := time.Parse("2006-01", m.selectedBillingMonth); err == nil {
					modeDisplay = t.Format("Jan 2006")
				} else {
					modeDisplay = m.selectedBillingMonth
				}
			}
		}
		leftContent.WriteString(labelStyle.Render("View: ") + valueStyle.Render("Billing - "+modeDisplay))
		leftContent.WriteString("\n")
		if m.billingBalance != nil {
			monthToDate := m.billingBalance.MonthToDateBalance
			if len(monthToDate) > leftPanelWidth-18 {
				monthToDate = truncateString(monthToDate, leftPanelWidth-18)
			}
			leftContent.WriteString(labelStyle.Render("Month-to-Date: ") + valueStyle.Render(monthToDate))
			leftContent.WriteString("\n")
			accountBalance := m.billingBalance.AccountBalance
			if len(accountBalance) > leftPanelWidth-18 {
				accountBalance = truncateString(accountBalance, leftPanelWidth-18)
			}
			leftContent.WriteString(labelStyle.Render("Account Balance: ") + valueStyle.Render(accountBalance))
			leftContent.WriteString("\n")
		}
		if m.billingMode == "invoices" {
			leftContent.WriteString(labelStyle.Render("Invoices: ") + valueStyle.Render(fmt.Sprintf("%d", len(m.billingInvoices))))
		} else {
			historyCount := 0
			if m.billingHistory != nil && m.billingHistory.BillingHistory != nil {
				historyCount = len(m.billingHistory.BillingHistory)
			}
			leftContent.WriteString(labelStyle.Render("History Entries: ") + valueStyle.Render(fmt.Sprintf("%d", historyCount)))
		}
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

	if m.account != nil && m.currentView != viewClusterResources {
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
	} else if m.currentView == viewClusters {
		rightContent.WriteString(keyStyle.Render("1") + " Droplets\n")
		rightContent.WriteString(keyStyle.Render("2") + " Clusters\n")
		rightContent.WriteString(keyStyle.Render("3") + " Billing\n")
		rightContent.WriteString(keyStyle.Render("r") + " Refresh\n")
		rightContent.WriteString(keyStyle.Render("enter") + " Enter\n")
		rightContent.WriteString(keyStyle.Render("q") + " Quit")
	} else if m.currentView == viewBilling {
		rightContent.WriteString(keyStyle.Render("1") + " Droplets\n")
		rightContent.WriteString(keyStyle.Render("2") + " Clusters\n")
		rightContent.WriteString(keyStyle.Render("3") + " Billing\n")
		rightContent.WriteString(keyStyle.Render("m") + " Monthly\n")
		rightContent.WriteString(keyStyle.Render("i") + " Invoices\n")
		rightContent.WriteString(keyStyle.Render("r") + " Refresh\n")
		if m.billingMode == "monthly" {
			if m.selectedBillingMonth == "" {
				rightContent.WriteString(keyStyle.Render("enter") + " Month Details\n")
			} else {
				rightContent.WriteString(keyStyle.Render("esc") + " Back\n")
			}
		}
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
		keybindings = keyStyle.Render("<1>") + " Droplets | " + keyStyle.Render("<2>") + " Clusters | " + keyStyle.Render("<3>") + " Billing | " + keyStyle.Render("<n>") + " New | " + keyStyle.Render("<r>") + " Refresh | " + keyStyle.Render("<d>") + " Delete | " + keyStyle.Render("<s>") + " SSH | " + keyStyle.Render("<q>") + " Quit"
	} else if m.currentView == viewClusters {
		keybindings = keyStyle.Render("<1>") + " Droplets | " + keyStyle.Render("<2>") + " Clusters | " + keyStyle.Render("<3>") + " Billing | " + keyStyle.Render("<r>") + " Refresh | " + keyStyle.Render("<enter>") + " Enter | " + keyStyle.Render("<q>") + " Quit"
	} else if m.currentView == viewBilling {
		keybindings = keyStyle.Render("<1>") + " Droplets | " + keyStyle.Render("<2>") + " Clusters | " + keyStyle.Render("<3>") + " Billing | " + keyStyle.Render("<m>") + " Monthly | " + keyStyle.Render("<i>") + " Invoices | " + keyStyle.Render("<r>") + " Refresh"
		if m.billingMode == "monthly" && m.selectedBillingMonth != "" {
			keybindings += " | " + keyStyle.Render("<esc>") + " Back"
		} else if m.billingMode == "monthly" {
			keybindings += " | " + keyStyle.Render("<enter>") + " Details"
		}
		keybindings += " | " + keyStyle.Render("<q>") + " Quit"
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

	accountInfo.WriteString(labelStyle.Render("Version: ") + valueStyle.Render("dogoctl v1.4.1"))

	// Render the left panel - ensure it's always visible with summary
	leftPanelContent := accountInfo.String()
	// CRITICAL: Ensure content is not empty
	if strings.TrimSpace(leftPanelContent) == "" {
		leftPanelContent = fmt.Sprintf("DigitalOcean\n\nDroplets: %d\nRegion: %s\nRefresh: N/A\nVersion: dogoctl v1.4.1",
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
	if m.currentView == viewBilling {
		statusText = "<billing>"
		historyCount := 0
		if m.billingHistory != nil && m.billingHistory.BillingHistory != nil {
			historyCount = len(m.billingHistory.BillingHistory)
		}
		if m.billingBalance != nil {
			statusText = fmt.Sprintf("%s | Balance: %s | MTD: %s | Invoices: %d",
				statusText, m.billingBalance.AccountBalance, m.billingBalance.MonthToDateBalance, len(m.billingInvoices))
		} else {
			statusText = fmt.Sprintf("%s | Invoices: %d | History: %d", statusText, len(m.billingInvoices), historyCount)
		}
	} else if m.currentView == viewClusterResources {
		// Show cluster resource view
		if m.selectedCluster != nil {
			statusText = fmt.Sprintf("<%s>", m.clusterResourceType)
			clusterName := m.selectedCluster.Name
			// Truncate cluster name if needed
			if len(clusterName) > 20 {
				clusterName = clusterName[:17] + "..."
			}
			statusText = fmt.Sprintf("%s | Cluster: %s | Resource: %s", statusText, clusterName, strings.Title(m.clusterResourceType))
		}
	} else if m.currentView == viewClusters {
		statusText = fmt.Sprintf("<clusters> | Clusters [%d]", m.clusterCount)
	} else {
		statusText = "<droplets>"
		region := m.selectedRegion
		// Truncate region if too long
		if len(region) > 15 {
			region = region[:12] + "..."
		}
		statusText = fmt.Sprintf("%s | Droplets(%s) [%d]", statusText, region, m.dropletCount)
	}

	if m.loading {
		statusText = fmt.Sprintf("%s | %s Loading...", statusText, m.spinner.View())
	}

	// Make status bar responsive to width
	width := m.width
	if width < 40 {
		width = 40
	}

	// Truncate if too long - be more aggressive with truncation
	maxStatusLen := width - 4
	if maxStatusLen < 20 {
		maxStatusLen = 20 // Minimum readable length
	}
	if len(statusText) > maxStatusLen {
		// Try to preserve important info (view type and count) while truncating
		if m.currentView == viewDroplets {
			// Preserve: "<droplets> | Droplets(...) [N]"
			shortText := fmt.Sprintf("<droplets> | [%d]", m.dropletCount)
			if len(shortText) <= maxStatusLen {
				statusText = shortText
			} else {
				statusText = statusText[:maxStatusLen-3] + "..."
			}
		} else if m.currentView == viewClusters {
			shortText := fmt.Sprintf("<clusters> | [%d]", m.clusterCount)
			if len(shortText) <= maxStatusLen {
				statusText = shortText
			} else {
				statusText = statusText[:maxStatusLen-3] + "..."
			}
		} else {
			statusText = statusText[:maxStatusLen-3] + "..."
		}
	}

	return lipgloss.NewStyle().
		Foreground(mutedColor).
		Background(bgColor).
		Padding(0, 1).
		Width(width).
		Render(statusText)
}

func (m model) renderSSHIPSelection() string {
	if m.selectedDroplet == nil {
		return ""
	}

	publicIP := getPublicIP(*m.selectedDroplet)
	privateIP := getPrivateIP(*m.selectedDroplet)

	var s strings.Builder
	// Note: Top padding is applied globally in View(), not here
	s.WriteString(headerStyle.Render("🔌 Select IP Address for SSH"))
	s.WriteString("\n\n")

	s.WriteString(fmt.Sprintf("Droplet: %s\n\n", m.selectedDroplet.Name))

	if publicIP != "" {
		selected := ""
		if m.sshIPType == "public" {
			selected = " ← "
		}
		publicStyle := lipgloss.NewStyle().Foreground(successColor).Bold(true)
		if m.sshIPType == "public" {
			publicStyle = publicStyle.Foreground(highlightColor)
		}
		s.WriteString(fmt.Sprintf("  %s %s Public IP:  %s%s\n",
			selected,
			keyStyle.Render("[1]"),
			publicStyle.Render(publicIP),
			selected))
	}

	if privateIP != "" {
		selected := ""
		if m.sshIPType == "private" {
			selected = " ← "
		}
		privateStyle := lipgloss.NewStyle().Foreground(successColor).Bold(true)
		if m.sshIPType == "private" {
			privateStyle = privateStyle.Foreground(highlightColor)
		}
		s.WriteString(fmt.Sprintf("  %s %s Private IP: %s%s\n",
			selected,
			keyStyle.Render("[2]"),
			privateStyle.Render(privateIP),
			selected))
	}

	s.WriteString("\n")
	s.WriteString(helpStyle.Render("Press [1] or [p] for Public IP | [2] or [r] for Private IP | [esc] to cancel"))

	content := lipgloss.NewStyle().
		Width(m.width-4).
		Padding(2, 4).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Render(s.String())

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
}

func (m model) renderSSHTerminal() string {
	var s strings.Builder

	// Show exit confirmation dialog if needed
	if m.sshTerminalConfirmExit {
		return m.renderSSHExitConfirmation()
	}

	// Prominent header showing connection info - make it very visible
	hostInfo := fmt.Sprintf("🔌 Connected to: %s", m.sshTerminalHost)
	ipInfo := fmt.Sprintf("IP: %s", m.sshTerminalIP)

	headerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(primaryColor).
		BorderTop(true).
		BorderBottom(true).
		BorderLeft(true).
		BorderRight(true).
		Padding(1, 2).
		Width(m.width - 2).
		Foreground(lipgloss.Color("255")).
		Background(primaryColor).
		Bold(true)

	hostStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("255")).
		Bold(true).
		Background(primaryColor)

	ipStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("229")).
		Background(primaryColor)

	headerContent := fmt.Sprintf("%s  |  %s",
		hostStyle.Render(hostInfo),
		ipStyle.Render(ipInfo))

	headerBox := headerStyle.Render(headerContent)
	s.WriteString(headerBox)
	s.WriteString("\n")

	// Terminal output area - reserve space for header and padding
	availableHeight := m.height - getTopPadding() - 6 // Header + padding + help text
	if availableHeight < 5 {
		availableHeight = 5
	}

	// CRITICAL: Calculate display dimensions - must match PTY and emulator sizes exactly
	displayCols := m.width - 4 // Account for border and padding
	displayRows := availableHeight

	// Render terminal output - use terminal emulator as SINGLE SOURCE OF TRUTH
	// CRITICAL: The emulator IS the screen buffer - render it exactly as-is
	// No line manipulation, no splitting, no padding - just render the buffer directly
	m.sshTerminalMutex.Lock()
	var displayContent string

	// Use terminal emulator as SINGLE SOURCE OF TRUTH for rendering
	// The emulator maintains a cell-by-cell screen buffer
	if m.sshTerminalEmulator != nil {
		// CRITICAL: Ensure emulator size matches display area exactly BEFORE getting display
		// Size mismatch causes cursor positioning errors, broken rendering, etc.
		currentWidth, currentHeight := m.sshTerminalEmulator.Dimensions()
		if currentWidth != displayCols || currentHeight != displayRows {
			// Resize emulator to match display area exactly
			m.sshTerminalEmulator.Resize(displayCols, displayRows)
		}

		// Get the screen buffer directly from emulator
		// GetDisplayWithColors() returns the screen state with ANSI color codes
		// The emulator maintains a cell-by-cell screen buffer of size displayRows x displayCols
		//
		// CRITICAL: The emulator processes ALL escape sequences:
		// - Newlines (\n) - moves cursor to next line, creates new row in buffer
		// - Carriage returns (\r) - moves cursor to start of current line
		// - Line clearing (ESC[K, ESC[2K) - clears line content
		// - Cursor positioning - all handled by emulator
		//
		// GetDisplayWithColors() returns a string representation of the screen buffer
		// The format should be: each screen row as one line, separated by \n
		// Each line contains ANSI color codes and is exactly displayCols characters
		displayContent = m.sshTerminalEmulator.GetDisplayWithColors()

		// CRITICAL: Verify and ensure proper line structure
		// GetDisplayWithColors() should return displayRows lines separated by \n
		// If it doesn't, we need to fix it
		lines := strings.Split(displayContent, "\n")

		// Remove empty trailing line if present
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}

		// CRITICAL: We MUST have exactly displayRows lines
		// Each line represents one screen row - missing lines cause rendering issues
		if len(lines) < displayRows {
			// Pad with empty lines (screen hasn't filled all rows yet)
			for len(lines) < displayRows {
				lines = append(lines, "")
			}
		} else if len(lines) > displayRows {
			// Take only the last displayRows lines (most recent screen content)
			lines = lines[len(lines)-displayRows:]
		}

		// Join with \n - CRITICAL: This creates visual line breaks
		// Without \n separators, lines will overlap and render incorrectly
		// Each \n in the final output creates a new line in the terminal
		displayContent = strings.Join(lines, "\n")
	} else {
		// Emulator not initialized yet - show connection message
		displayContent = fmt.Sprintf("🔌 Connecting to %s (%s)...", m.sshTerminalHost, m.sshTerminalIP)
		// Pad to fill available space (only for connection message, not terminal output)
		lines := []string{displayContent}
		for len(lines) < displayRows {
			lines = append(lines, "")
		}
		displayContent = strings.Join(lines, "\n")
	}
	m.sshTerminalMutex.Unlock()

	// displayContent is the screen buffer from terminal emulator
	// It contains ANSI color codes and represents the current terminal state
	// Each line is exactly displayCols characters wide (from emulator)
	// Lines are separated by \n
	//
	// CRITICAL: Render with minimal lipgloss styling to preserve newlines
	// Padding(1, 1) should preserve newlines, but we'll render directly
	// The emulator buffer already has the correct format with proper newlines
	terminalBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 1).
		Render(displayContent)

	s.WriteString(terminalBox)
	s.WriteString("\n\n")

	// Help text with host reminder
	hostReminder := fmt.Sprintf("Host: %s", m.sshTerminalHost)
	helpText := helpStyle.Render(fmt.Sprintf("%s  |  [ctrl+c] Terminate Process  [esc] Exit SSH",
		hostReminder))
	s.WriteString(helpText)
	s.WriteString("\n")

	return s.String()
}

// renderSSHExitConfirmation renders the exit confirmation dialog
func (m model) renderSSHExitConfirmation() string {
	var s strings.Builder

	// Prominent header still visible
	hostInfo := fmt.Sprintf("🔌 Connected to: %s", m.sshTerminalHost)
	ipInfo := fmt.Sprintf("IP: %s", m.sshTerminalIP)

	headerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(primaryColor).
		BorderTop(true).
		BorderBottom(true).
		BorderLeft(true).
		BorderRight(true).
		Padding(1, 2).
		Width(m.width - 2).
		Foreground(lipgloss.Color("255")).
		Background(primaryColor).
		Bold(true)

	hostStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("255")).
		Bold(true).
		Background(primaryColor)

	ipStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("229")).
		Background(primaryColor)

	headerContent := fmt.Sprintf("%s  |  %s",
		hostStyle.Render(hostInfo),
		ipStyle.Render(ipInfo))

	headerBox := headerStyle.Render(headerContent)
	s.WriteString(headerBox)
	s.WriteString("\n\n")

	// Confirmation dialog
	confirmText := fmt.Sprintf("⚠️  Close SSH connection to %s?", m.sshTerminalHost)
	confirmBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(warningColor).
		Padding(2, 4).
		Width(m.width - 4).
		Render(fmt.Sprintf("%s\n\n%s\n\n%s",
			confirmText,
			keyStyle.Render("[y]")+" Yes, close connection  |  "+keyStyle.Render("[n]")+" No, cancel",
			keyStyle.Render("[esc]")+" Cancel"))

	s.WriteString(lipgloss.Place(m.width, m.height-6, lipgloss.Center, lipgloss.Center, confirmBox))

	return s.String()
}

func (m model) renderDeleteConfirmation() string {
	var s strings.Builder
	// Note: Top padding is applied globally in View(), not here

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
	// If in selection mode, show selection table
	if m.selectingRegion || m.selectingSize || m.selectingImage {
		return m.renderSelectionView()
	}

	var s strings.Builder
	// Note: Top padding is applied globally in View(), not here

	formTitle := headerStyle.Render("✨ Create New Droplet")
	s.WriteString(formTitle)
	s.WriteString("\n\n")

	// Name field
	labelStyle := lipgloss.NewStyle().Width(20).Foreground(mutedColor)
	if m.inputIndex == 0 {
		labelStyle = labelStyle.Foreground(primaryColor).Bold(true)
	}
	s.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Name:"), m.nameInput.View()))
	if m.inputIndex == 0 {
		s.WriteString(fmt.Sprintf("   %s\n", lipgloss.NewStyle().Foreground(mutedColor).Italic(true).Render("e.g., my-droplet")))
	}
	s.WriteString("\n")

	// Region field (selection)
	regionLabelStyle := lipgloss.NewStyle().Width(20).Foreground(mutedColor)
	if m.inputIndex == 1 {
		regionLabelStyle = regionLabelStyle.Foreground(primaryColor).Bold(true)
	}
	regionValue := m.selectedRegionSlug
	if regionValue == "" {
		regionValue = lipgloss.NewStyle().Foreground(mutedColor).Italic(true).Render("Press Enter to select")
	} else {
		regionValue = lipgloss.NewStyle().Foreground(successColor).Render(regionValue)
	}
	s.WriteString(fmt.Sprintf("%s %s\n", regionLabelStyle.Render("Region:"), regionValue))
	if m.inputIndex == 1 {
		s.WriteString(fmt.Sprintf("   %s\n", lipgloss.NewStyle().Foreground(mutedColor).Italic(true).Render("Press Enter to select from list")))
	}
	s.WriteString("\n")

	// Size field (selection)
	sizeLabelStyle := lipgloss.NewStyle().Width(20).Foreground(mutedColor)
	if m.inputIndex == 2 {
		sizeLabelStyle = sizeLabelStyle.Foreground(primaryColor).Bold(true)
	}
	sizeValue := m.selectedSizeSlug
	if sizeValue == "" {
		sizeValue = lipgloss.NewStyle().Foreground(mutedColor).Italic(true).Render("Press Enter to select")
	} else {
		sizeValue = lipgloss.NewStyle().Foreground(successColor).Render(sizeValue)
	}
	s.WriteString(fmt.Sprintf("%s %s\n", sizeLabelStyle.Render("Size:"), sizeValue))
	if m.inputIndex == 2 {
		s.WriteString(fmt.Sprintf("   %s\n", lipgloss.NewStyle().Foreground(mutedColor).Italic(true).Render("Press Enter to select from list")))
	}
	s.WriteString("\n")

	// Image field (selection)
	imageLabelStyle := lipgloss.NewStyle().Width(20).Foreground(mutedColor)
	if m.inputIndex == 3 {
		imageLabelStyle = imageLabelStyle.Foreground(primaryColor).Bold(true)
	}
	imageValue := m.selectedImageSlug
	if imageValue == "" {
		imageValue = lipgloss.NewStyle().Foreground(mutedColor).Italic(true).Render("Press Enter to select")
	} else {
		imageValue = lipgloss.NewStyle().Foreground(successColor).Render(imageValue)
	}
	s.WriteString(fmt.Sprintf("%s %s\n", imageLabelStyle.Render("Image:"), imageValue))
	if m.inputIndex == 3 {
		s.WriteString(fmt.Sprintf("   %s\n", lipgloss.NewStyle().Foreground(mutedColor).Italic(true).Render("Press Enter to select from list")))
	}
	s.WriteString("\n")

	// Tags field
	tagsLabelStyle := lipgloss.NewStyle().Width(20).Foreground(mutedColor)
	if m.inputIndex == 4 {
		tagsLabelStyle = tagsLabelStyle.Foreground(primaryColor).Bold(true)
	}
	s.WriteString(fmt.Sprintf("%s %s\n", tagsLabelStyle.Render("Tags:"), m.tagsInput.View()))
	if m.inputIndex == 4 {
		s.WriteString(fmt.Sprintf("   %s\n", lipgloss.NewStyle().Foreground(mutedColor).Italic(true).Render("comma-separated, e.g., web,production")))
	}
	s.WriteString("\n")

	if m.loading {
		s.WriteString(fmt.Sprintf("  %s Creating droplet...\n\n", m.spinner.View()))
	}

	if m.err != nil {
		s.WriteString(errorMessageStyle.Render(fmt.Sprintf("❌ Error: %v", m.err)))
		s.WriteString("\n")
	}

	helpText := helpStyle.Render("[tab] Next  [shift+tab] Previous  [enter] Select/Create  [esc] Cancel")
	s.WriteString(helpText)
	s.WriteString("\n")

	return s.String()
}

func (m model) renderSelectionView() string {
	var s strings.Builder
	// Note: Top padding is applied globally in View(), not here

	var title string
	if m.selectingRegion {
		title = headerStyle.Render("📍 Select Region")
	} else if m.selectingSize {
		title = headerStyle.Render("💾 Select Size")
	} else if m.selectingImage {
		title = headerStyle.Render("🖼️  Select Image")
	}
	s.WriteString(title)
	s.WriteString("\n\n")

	// Render selection table
	s.WriteString(m.selectionTable.View())
	s.WriteString("\n\n")

	helpText := helpStyle.Render("[↑/↓] Navigate  [enter] Select  [esc] Cancel")
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
	// Note: Top padding is applied globally in View(), not here

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

	// IP Addresses - show public and private separately
	publicIP := getPublicIP(*d)
	privateIP := getPrivateIP(*d)

	if publicIP != "" {
		details = append(details, struct {
			label string
			value string
		}{"🌐 Public IP:", publicIP})
	}
	if privateIP != "" {
		details = append(details, struct {
			label string
			value string
		}{"🔒 Private IP:", privateIP})
	}

	// Show IPv6 if available
	var ipv6s []string
	for _, v6 := range d.Networks.V6 {
		ipv6s = append(ipv6s, v6.IPAddress)
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

	// Show SSH option if droplet is active and has IP addresses (publicIP and privateIP already declared above)
	helpText := helpStyle.Render("[esc/enter] Back  [q] Quit")
	if d.Status == "active" && (publicIP != "" || privateIP != "") {
		helpText = helpStyle.Render("[esc/enter] Back  [s] SSH  [q] Quit")
	}
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
	// Note: Top padding is applied globally in View(), not here

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

func (m model) renderBillingDetails() string {
	var s strings.Builder
	// Note: Top padding is applied globally in View(), not here

	labelStyle := lipgloss.NewStyle().Foreground(primaryColor).Bold(true).Width(20)
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("255"))

	// Dynamic box width based on terminal size
	boxWidth := min(m.width-4, 90)
	if boxWidth < 50 {
		boxWidth = 50
	}
	if boxWidth > m.width-4 {
		boxWidth = m.width - 4
	}

	// Calculate available height for content (terminal height minus top padding, header and help text)
	availableHeight := m.height - getTopPadding() - 6 // Reserve space for top padding, header, padding, and help text
	if availableHeight < 5 {
		availableHeight = 5
	}

	if m.detailedInvoice != nil && m.selectedInvoice != nil {
		// Render full invoice details (like doctl invoice get)
		inv := m.detailedInvoice
		invMeta := m.selectedInvoice
		headerText := fmt.Sprintf("💰 Invoice Details")

		headerBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor).
			Padding(1, 2).
			Width(boxWidth).
			Render(headerText)

		s.WriteString(headerBox)
		s.WriteString("\n\n")

		var details strings.Builder
		details.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Invoice UUID:"), valueStyle.Render(invMeta.InvoiceUUID)))
		details.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Amount:"), valueStyle.Render(invMeta.Amount)))
		if invMeta.InvoicePeriod != "" {
			details.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Invoice Period:"), valueStyle.Render(invMeta.InvoicePeriod)))
		}
		if !invMeta.UpdatedAt.IsZero() {
			details.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Updated At:"), valueStyle.Render(invMeta.UpdatedAt.Format("2006-01-02 15:04:05"))))
		}

		// Invoice Items
		if len(inv.InvoiceItems) > 0 {
			details.WriteString("\n")
			details.WriteString(labelStyle.Render("Invoice Items:"))
			details.WriteString("\n")
			totalAmount := 0.0
			for i, item := range inv.InvoiceItems {
				details.WriteString(fmt.Sprintf("  %d. %s\n", i+1, valueStyle.Render(item.Product)))
				if item.GroupDescription != "" {
					details.WriteString(fmt.Sprintf("     %s: %s\n", labelStyle.Render("Group"), valueStyle.Render(item.GroupDescription)))
				}
				if item.Description != "" {
					descLines := strings.Split(item.Description, "\n")
					for _, line := range descLines {
						if strings.TrimSpace(line) != "" {
							details.WriteString(fmt.Sprintf("     %s\n", valueStyle.Render("  "+line)))
						}
					}
				}
				if item.ResourceUUID != "" {
					details.WriteString(fmt.Sprintf("     %s: %s\n", labelStyle.Render("Resource UUID"), valueStyle.Render(item.ResourceUUID)))
				}
				if item.ResourceID != "" {
					details.WriteString(fmt.Sprintf("     %s: %s\n", labelStyle.Render("Resource ID"), valueStyle.Render(item.ResourceID)))
				}
				details.WriteString(fmt.Sprintf("     %s: %s\n", labelStyle.Render("Amount"), valueStyle.Render(item.Amount)))
				if item.Duration != "" {
					details.WriteString(fmt.Sprintf("     %s: %s %s\n", labelStyle.Render("Duration"), valueStyle.Render(item.Duration), valueStyle.Render(item.DurationUnit)))
				}
				if !item.StartTime.IsZero() && !item.EndTime.IsZero() {
					details.WriteString(fmt.Sprintf("     %s: %s to %s\n", labelStyle.Render("Period"),
						valueStyle.Render(item.StartTime.Format("2006-01-02 15:04")),
						valueStyle.Render(item.EndTime.Format("2006-01-02 15:04"))))
				}
				if item.Category != "" {
					details.WriteString(fmt.Sprintf("     %s: %s\n", labelStyle.Render("Category"), valueStyle.Render(item.Category)))
				}
				if item.ProjectName != "" {
					details.WriteString(fmt.Sprintf("     %s: %s\n", labelStyle.Render("Project"), valueStyle.Render(item.ProjectName)))
				}
				// Calculate total
				amountVal := parseAmount(item.Amount)
				totalAmount += amountVal
				if i < len(inv.InvoiceItems)-1 {
					details.WriteString("\n")
				}
			}
			details.WriteString("\n")
			details.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Total Items:"), valueStyle.Render(fmt.Sprintf("%d", len(inv.InvoiceItems)))))
			details.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Calculated Total:"), valueStyle.Render(formatAmount(totalAmount))))
		}

		// Convert details to lines for scrolling
		allLines := strings.Split(details.String(), "\n")
		totalLines := len(allLines)

		// Limit scroll position
		maxScroll := max(0, totalLines-availableHeight)
		if m.billingDetailsScroll > maxScroll {
			m.billingDetailsScroll = maxScroll
		}
		if m.billingDetailsScroll < 0 {
			m.billingDetailsScroll = 0
		}

		// Get visible lines
		startLine := m.billingDetailsScroll
		endLine := min(startLine+availableHeight, totalLines)
		visibleLines := allLines[startLine:endLine]
		visibleContent := strings.Join(visibleLines, "\n")

		detailsBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor).
			Padding(1, 2).
			Width(boxWidth).
			Height(availableHeight + 2) // Add padding

		s.WriteString(detailsBox.Render(visibleContent))
		s.WriteString("\n\n")

		// Scroll indicator
		scrollInfo := ""
		if totalLines > availableHeight {
			scrollInfo = fmt.Sprintf(" [%d/%d lines]", m.billingDetailsScroll+1, totalLines)
		}
		helpText := helpStyle.Render(fmt.Sprintf("[↑↓/j/k] Scroll  [g/G] Top/Bottom  [esc/enter] Back  [q] Quit%s", scrollInfo))
		s.WriteString(helpText)
		s.WriteString("\n")

	} else if m.selectedInvoice != nil {
		// Fallback: Render basic invoice details if detailed invoice not loaded yet
		inv := m.selectedInvoice
		headerText := fmt.Sprintf("💰 Invoice Details")

		headerBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor).
			Padding(1, 2).
			Width(boxWidth).
			Render(headerText)

		s.WriteString(headerBox)
		s.WriteString("\n\n")

		var details strings.Builder
		details.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Invoice UUID:"), valueStyle.Render(inv.InvoiceUUID)))
		details.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Amount:"), valueStyle.Render(inv.Amount)))
		details.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Invoice Period:"), valueStyle.Render(inv.InvoicePeriod)))
		if !inv.UpdatedAt.IsZero() {
			details.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Updated At:"), valueStyle.Render(inv.UpdatedAt.Format("2006-01-02 15:04:05"))))
		}

		// Convert details to lines for scrolling
		allLines := strings.Split(details.String(), "\n")
		totalLines := len(allLines)

		// Limit scroll position
		maxScroll := max(0, totalLines-availableHeight)
		if m.billingDetailsScroll > maxScroll {
			m.billingDetailsScroll = maxScroll
		}
		if m.billingDetailsScroll < 0 {
			m.billingDetailsScroll = 0
		}

		// Get visible lines
		startLine := m.billingDetailsScroll
		endLine := min(startLine+availableHeight, totalLines)
		visibleLines := allLines[startLine:endLine]
		visibleContent := strings.Join(visibleLines, "\n")

		detailsBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor).
			Padding(1, 2).
			Width(boxWidth).
			Height(availableHeight + 2) // Add padding

		s.WriteString(detailsBox.Render(visibleContent))
		s.WriteString("\n\n")

		// Scroll indicator
		scrollInfo := ""
		if totalLines > availableHeight {
			scrollInfo = fmt.Sprintf(" [%d/%d lines]", m.billingDetailsScroll+1, totalLines)
		}
		helpText := helpStyle.Render(fmt.Sprintf("[↑↓/j/k] Scroll  [g/G] Top/Bottom  [esc/enter] Back  [q] Quit%s", scrollInfo))
		s.WriteString(helpText)
		s.WriteString("\n")

	} else if m.selectedBillingEntry != nil {
		// Render billing entry details
		entry := m.selectedBillingEntry
		headerText := fmt.Sprintf("📊 Billing Entry Details")

		headerBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor).
			Padding(1, 2).
			Width(boxWidth).
			Render(headerText)

		s.WriteString(headerBox)
		s.WriteString("\n\n")

		var details strings.Builder
		details.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Date:"), valueStyle.Render(entry.Date.Format("2006-01-02 15:04:05"))))
		details.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Description:"), valueStyle.Render(entry.Description)))
		details.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Amount:"), valueStyle.Render(entry.Amount)))
		details.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Type:"), valueStyle.Render(entry.Type)))
		if entry.InvoiceUUID != nil && *entry.InvoiceUUID != "" {
			details.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Invoice UUID:"), valueStyle.Render(*entry.InvoiceUUID)))
		}
		if entry.InvoiceID != nil && *entry.InvoiceID != "" {
			details.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Invoice ID:"), valueStyle.Render(*entry.InvoiceID)))
		}

		// Convert details to lines for scrolling
		allLines := strings.Split(details.String(), "\n")
		totalLines := len(allLines)

		// Limit scroll position
		maxScroll := max(0, totalLines-availableHeight)
		if m.billingDetailsScroll > maxScroll {
			m.billingDetailsScroll = maxScroll
		}
		if m.billingDetailsScroll < 0 {
			m.billingDetailsScroll = 0
		}

		// Get visible lines
		startLine := m.billingDetailsScroll
		endLine := min(startLine+availableHeight, totalLines)
		visibleLines := allLines[startLine:endLine]
		visibleContent := strings.Join(visibleLines, "\n")

		detailsBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor).
			Padding(1, 2).
			Width(boxWidth).
			Height(availableHeight + 2) // Add padding

		s.WriteString(detailsBox.Render(visibleContent))
		s.WriteString("\n\n")

		// Scroll indicator
		scrollInfo := ""
		if totalLines > availableHeight {
			scrollInfo = fmt.Sprintf(" [%d/%d lines]", m.billingDetailsScroll+1, totalLines)
		}
		helpText := helpStyle.Render(fmt.Sprintf("[↑↓/j/k] Scroll  [g/G] Top/Bottom  [esc/enter] Back  [q] Quit%s", scrollInfo))
		s.WriteString(helpText)
		s.WriteString("\n")
	}

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
						"name":    n.Name,
						"status":  status,
						"roles":   roles,
						"age":     age,
						"version": n.Status.NodeInfo.KubeletVersion,
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

func loadRegions(client *godo.Client) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		opt := &godo.ListOptions{PerPage: 200}
		regions, _, err := client.Regions.List(ctx, opt)
		if err != nil {
			return errMsg(err)
		}
		return regionsLoadedMsg(regions)
	}
}

func loadSizes(client *godo.Client) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		opt := &godo.ListOptions{PerPage: 200}
		sizes, _, err := client.Sizes.List(ctx, opt)
		if err != nil {
			return errMsg(err)
		}
		return sizesLoadedMsg(sizes)
	}
}

func loadBalance(client *godo.Client) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		balance, _, err := client.Balance.Get(ctx)
		if err != nil {
			return errMsg(err)
		}
		return balanceLoadedMsg(balance)
	}
}

func loadInvoices(client *godo.Client) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		opt := &godo.ListOptions{Page: 1, PerPage: 50}
		var allInvoices []godo.InvoiceListItem

		// Handle pagination to get all invoices
		for {
			invoiceList, resp, err := client.Invoices.List(ctx, opt)
			if err != nil {
				return errMsg(err)
			}

			if invoiceList != nil {
				// Add invoices from current page
				allInvoices = append(allInvoices, invoiceList.Invoices...)

				// Also include the preview if available (only on first page)
				if opt.Page == 1 && invoiceList.InvoicePreview.InvoiceUUID != "" {
					allInvoices = append([]godo.InvoiceListItem{invoiceList.InvoicePreview}, allInvoices...)
				}
			}

			// Check if there are more pages
			if resp == nil || resp.Links == nil || resp.Links.IsLastPage() {
				break
			}

			// Get next page
			page, err := resp.Links.CurrentPage()
			if err != nil {
				break
			}
			opt.Page = page + 1
		}

		return invoicesLoadedMsg(allInvoices)
	}
}

func loadBillingHistory(client *godo.Client) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		opt := &godo.ListOptions{PerPage: 100}
		history, _, err := client.BillingHistory.List(ctx, opt)
		if err != nil {
			return errMsg(err)
		}
		return billingHistoryLoadedMsg(history)
	}
}

func loadInvoiceDetails(client *godo.Client, invoiceUUID string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		opt := &godo.ListOptions{PerPage: 100}
		invoice, _, err := client.Invoices.Get(ctx, invoiceUUID, opt)
		if err != nil {
			return errMsg(err)
		}
		return invoiceDetailsLoadedMsg(invoice)
	}
}

func loadImages(client *godo.Client) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		opt := &godo.ListOptions{PerPage: 200}
		images, _, err := client.Images.List(ctx, opt)
		if err != nil {
			return errMsg(err)
		}
		// Filter to only specific distributions: Ubuntu, Fedora, Debian, CentOS, AlmaLinux, Rocky Linux
		// Only x86/x64 architectures
		// Based on actual doctl output: slugs like "ubuntu-22-04-x64", "debian-12-x64", "rockylinux-9-x64", etc.
		allowedDistSlugPrefixes := []string{
			"ubuntu-",
			"fedora-",
			"debian-",
			"centos-",        // Matches "centos-stream-9-x64"
			"centos-stream-", // Explicit match for centos-stream
			"almalinux-",
			"rockylinux-",
		}

		allowedDistNames := []string{
			"ubuntu",
			"fedora",
			"debian",
			"centos",
			"almalinux",
			"rocky linux", // Distribution field uses "Rocky Linux" with space
		}

		var validImages []godo.Image
		for _, img := range images {
			// Must have a slug
			if img.Slug == "" {
				continue
			}

			slugLower := strings.ToLower(img.Slug)

			// Exclude ARM architectures
			if strings.Contains(slugLower, "-arm") || strings.Contains(slugLower, "_arm") ||
				strings.Contains(slugLower, "-aarch64") || strings.Contains(slugLower, "_aarch64") {
				continue
			}

			// Exclude GPU images
			if strings.HasPrefix(slugLower, "gpu-") {
				continue
			}

			// Check if slug starts with any allowed distribution prefix
			slugMatches := false
			for _, prefix := range allowedDistSlugPrefixes {
				if strings.HasPrefix(slugLower, prefix) {
					slugMatches = true
					break
				}
			}

			// Also check distribution field as fallback
			distLower := strings.ToLower(strings.TrimSpace(img.Distribution))
			distMatches := false
			if !slugMatches && distLower != "" {
				for _, allowedDist := range allowedDistNames {
					if distLower == allowedDist ||
						strings.Contains(distLower, allowedDist) ||
						(allowedDist == "rocky linux" && strings.Contains(distLower, "rocky")) ||
						(allowedDist == "almalinux" && strings.Contains(distLower, "alma")) {
						distMatches = true
						break
					}
				}
			}

			// Must match either slug or distribution
			if !slugMatches && !distMatches {
				continue
			}

			// Must be x86 or x64 architecture (all distribution images should have this in slug)
			// Check slug first (most reliable)
			hasX64 := strings.Contains(slugLower, "-x64") || strings.Contains(slugLower, "_x64") ||
				strings.Contains(slugLower, "-amd64") || strings.Contains(slugLower, "_amd64")
			hasX86 := strings.Contains(slugLower, "-x86") || strings.Contains(slugLower, "_x86")

			// If not found in slug, check name as fallback
			if !hasX64 && !hasX86 {
				nameLower := strings.ToLower(img.Name)
				hasX64 = strings.Contains(nameLower, "x64") || strings.Contains(nameLower, "amd64")
				hasX86 = strings.Contains(nameLower, "x86")
			}

			// Include if it has x86 or x64 architecture
			if hasX64 || hasX86 {
				validImages = append(validImages, img)
			}
		}
		return imagesLoadedMsg(validImages)
	}
}

func createDroplet(client *godo.Client, m model) tea.Cmd {
	return func() tea.Msg {
		name := strings.TrimSpace(m.nameInput.Value())
		region := m.selectedRegionSlug
		size := m.selectedSizeSlug
		image := m.selectedImageSlug
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
		droplet, resp, err := client.Droplets.Create(ctx, createRequest)
		if err != nil {
			// Try to extract more detailed error message from response
			var errMsgText string
			if resp != nil && resp.Body != nil {
				// The godo library should include error details in the error message
				errMsgText = err.Error()
			} else {
				errMsgText = err.Error()
			}
			// Add context about what was being created
			return errMsg(fmt.Errorf("failed to create droplet: %s (region: %s, size: %s, image: %s)",
				errMsgText, region, size, image))
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

// startSSHTerminalView starts the SSH terminal view
func (m *model) startSSHTerminalView(ip, name string) (tea.Model, tea.Cmd) {
	m.sshTerminalActive = true
	m.sshTerminalHost = name
	m.sshTerminalIP = ip
	// Initialize terminal emulator - this is the SINGLE SOURCE OF TRUTH for rendering
	// Initialize raw output buffer if needed
	if m.sshTerminalRawOutput == nil {
		m.sshTerminalRawOutput = &strings.Builder{}
	} else {
		m.sshTerminalRawOutput.Reset()
	}

	// CRITICAL: Initialize terminal emulator with correct size
	// Calculate available display size (must match what we'll render)
	availableRows := m.height - getTopPadding() - 6 // Header + padding + help text
	if availableRows < 5 {
		availableRows = 5
	}
	availableCols := m.width - 4 // Account for border and padding
	if availableCols < 40 {
		availableCols = 40
	}
	// Create terminal buffer with exact display dimensions
	// This ensures cursor positioning and screen updates work correctly
	m.sshTerminalEmulator = terminal.NewTerminalBuffer(availableCols, availableRows)
	m.sshTerminalParser = govte.NewParser()

	// Start SSH connection - the started message will trigger output polling
	return m, startSSHTerminal(ip, name, m.sshOutputChan)
}

// startSSHTerminal starts an SSH connection in a PTY
func startSSHTerminal(ip, name string, outputChan chan<- tea.Msg) tea.Cmd {
	return func() tea.Msg {
		// Create SSH command with options to avoid hanging
		cmd := exec.Command("ssh",
			"-tt", // Force TTY allocation for interactive programs (needed for htop)
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "LogLevel=ERROR",
			ip)

		// CRITICAL: Set TERM environment variable for proper terminal emulation
		// ncurses apps like htop need this to know terminal capabilities
		// Use xterm-256color for full color and capability support
		termType := os.Getenv("TERM")
		if termType == "" {
			// Default to xterm-256color if TERM is not set
			termType = "xterm-256color"
		}
		// Ensure we use a terminal type that supports full capabilities
		// xterm-256color is widely supported and has all features needed for htop
		if termType != "xterm-256color" && termType != "screen-256color" && termType != "tmux-256color" {
			termType = "xterm-256color"
		}
		cmd.Env = append(os.Environ(), "TERM="+termType)

		// Create PTY with proper initial size
		// The size will be updated when we get the actual window size
		// Use reasonable defaults that will be updated immediately
		ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
			Rows: 24, // Will be updated on first WindowSizeMsg
			Cols: 80, // Will be updated on first WindowSizeMsg
		})
		if err != nil {
			return errMsg(fmt.Errorf("failed to start SSH: %v", err))
		}

		// Start reading output in a goroutine immediately
		go func() {
			defer func() {
				if r := recover(); r != nil {
					outputChan <- errMsg(fmt.Errorf("panic in SSH reader: %v", r))
				}
			}()

			// Read directly from PTY - preserve ALL bytes including ANSI sequences
			buf := make([]byte, 4096) // Larger buffer for better performance
			for {
				n, err := ptmx.Read(buf)
				if err != nil {
					if err == io.EOF {
						outputChan <- sshTerminalOutputMsg("\r\n[Connection closed]\r\n")
						outputChan <- sshTerminalClosedMsg{}
						return
					}
					outputChan <- errMsg(fmt.Errorf("SSH read error: %v", err))
					return
				}
				if n > 0 {
					// Send output immediately - preserve ALL bytes (ANSI sequences, control chars, etc.)
					outputChan <- sshTerminalOutputMsg(string(buf[:n]))
				}
			}
		}()

		// Return the PTY and command for the model to store
		return sshTerminalStartedMsg{ptmx: ptmx, cmd: cmd, ip: ip, name: name}
	}
}

// sshTerminalStartedMsg is sent when SSH terminal starts
type sshTerminalStartedMsg struct {
	ptmx *os.File
	cmd  *exec.Cmd
	ip   string
	name string
}

// waitForSSHOutput waits for messages from the SSH output channel
// Uses a continuous ticker to poll the channel non-blockingly
func waitForSSHOutput(outputChan <-chan tea.Msg) tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		select {
		case msg := <-outputChan:
			// Got a message, return it
			return msg
		default:
			// No message yet, return time to keep ticker running
			return t
		}
	})
}

// sshTerminalClosedMsg is sent when SSH connection closes
type sshTerminalClosedMsg struct{}

// updateSSHTerminal handles keyboard input for SSH terminal
func (m *model) updateSSHTerminal(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle exit confirmation dialog
	if m.sshTerminalConfirmExit {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "y", "Y":
				// Confirm exit
				m.sshTerminalConfirmExit = false
				m.closeSSHTerminal()
				return m, nil
			case "n", "N", "esc":
				// Cancel exit
				m.sshTerminalConfirmExit = false
				return m, nil
			default:
				return m, nil
			}
		default:
			return m, nil
		}
	}

	// Forward all other input to SSH
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		return m.handleSSHInput(keyMsg)
	}

	return m, nil
}

// writeToPTY writes bytes to the SSH PTY, handling errors gracefully
func (m *model) writeToPTY(b []byte) error {
	if m.sshTerminalPTY == nil {
		return fmt.Errorf("PTY is nil")
	}
	_, err := m.sshTerminalPTY.Write(b)
	if err != nil {
		// PTY write failed - connection might be closed
		m.err = fmt.Errorf("failed to write to PTY: %v", err)
		m.closeSSHTerminal()
		return err
	}
	return nil
}

// handleSSHInput forwards keyboard input to the SSH session
func (m *model) handleSSHInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle Esc key - show exit confirmation dialog
	if msg.Type == tea.KeyEsc || msg.String() == "esc" {
		if !m.sshTerminalConfirmExit {
			m.sshTerminalConfirmExit = true
			return m, nil
		}
		// If already in confirmation, cancel it
		m.sshTerminalConfirmExit = false
		return m, nil
	}

	// NO LOCAL SCROLLING - terminal emulator IS the screen buffer
	// All keys (including pageup/pagedown) are forwarded to SSH
	// The terminal emulator handles its own scrollback internally

	// All other keys are forwarded to SSH
	// This includes: regular typing, Enter, Backspace, Tab, arrows, function keys, etc.

	if m.sshTerminalPTY == nil {
		// PTY not ready yet - ignore input
		return m, nil
	}

	var keyBytes []byte

	// CRITICAL: Check for special control keys FIRST
	// These must be handled before checking runes
	switch msg.Type {
	case tea.KeyEnter:
		// Enter key - send carriage return to execute command
		keyBytes = []byte("\r")
	case tea.KeyBackspace:
		// Backspace - send DEL (0x7f) for proper terminal behavior
		// Note: \b (0x08) is backspace, but terminals expect DEL (0x7f)
		keyBytes = []byte{0x7f}
	case tea.KeyTab:
		keyBytes = []byte("\t")
	case tea.KeySpace:
		keyBytes = []byte(" ")
	case tea.KeyCtrlC:
		// Ctrl+C - send SIGINT (ASCII 3) to terminate remote process
		keyBytes = []byte{3}
	case tea.KeyCtrlD:
		// Ctrl+D - send EOF (ASCII 4) to close input stream
		keyBytes = []byte{4}
	case tea.KeyCtrlZ:
		// Ctrl+Z - send SUSP (ASCII 26) to suspend process
		keyBytes = []byte{26}
	case tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight:
		// Arrow keys - send ANSI escape sequences
		// These are critical for command history and interactive programs like htop
		keyBytes = getANSISequence(msg.Type)
	case tea.KeyDelete:
		// Delete key - send ANSI sequence
		keyBytes = getANSISequence(msg.Type)
	case tea.KeyF1, tea.KeyF2, tea.KeyF3, tea.KeyF4, tea.KeyF5, tea.KeyF6,
		tea.KeyF7, tea.KeyF8, tea.KeyF9, tea.KeyF10, tea.KeyF11, tea.KeyF12:
		// Function keys - needed for htop, vim, etc.
		keyBytes = getFunctionKeySequence(msg.Type)
	default:
		// Regular character input - check for runes first
		if len(msg.Runes) > 0 {
			// Convert runes to UTF-8 bytes
			// This handles all printable characters: letters, numbers, symbols, etc.
			keyBytes = []byte(string(msg.Runes))
		} else {
			// Fallback: try string representation for single characters
			keyStr := msg.String()
			if keyStr != "" && len(keyStr) == 1 {
				keyBytes = []byte(keyStr)
			} else if msg.Type >= tea.KeyCtrlA && msg.Type <= tea.KeyCtrlZ {
				// Ctrl+letter combinations (Ctrl+A = 1, Ctrl+B = 2, etc.)
				keyBytes = []byte{byte(msg.Type - tea.KeyCtrlA + 1)}
			}
		}
	}

	// Write to PTY if we have bytes to send
	if len(keyBytes) > 0 {
		if err := m.writeToPTY(keyBytes); err != nil {
			// Error already handled in writeToPTY (closes terminal, sets error)
			return m, nil
		}
	}

	return m, nil
}

// closeSSHTerminal closes the SSH terminal connection
func (m *model) closeSSHTerminal() {
	if m.sshTerminalCmd != nil && m.sshTerminalCmd.Process != nil {
		m.sshTerminalCmd.Process.Kill()
	}
	if m.sshTerminalPTY != nil {
		m.sshTerminalPTY.Close()
	}
	m.sshTerminalActive = false
	m.sshTerminalEmulator = nil
	m.sshTerminalParser = nil
	m.sshTerminalPTY = nil
	m.sshTerminalCmd = nil
	if m.sshTerminalRawOutput != nil {
		m.sshTerminalRawOutput.Reset()
	}
	m.sshTerminalHost = ""
	m.sshTerminalIP = ""
	m.sshTerminalConfirmExit = false
	// Clear any pending messages from the channel
	for {
		select {
		case <-m.sshOutputChan:
		default:
			return
		}
	}
}

// getANSISequence returns ANSI escape sequence for special keys
func getANSISequence(key tea.KeyType) []byte {
	switch key {
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	default:
		return nil
	}
}

// getFunctionKeySequence returns ANSI escape sequence for function keys (F1-F12)
func getFunctionKeySequence(key tea.KeyType) []byte {
	switch key {
	case tea.KeyF1:
		return []byte("\x1bOP")
	case tea.KeyF2:
		return []byte("\x1bOQ")
	case tea.KeyF3:
		return []byte("\x1bOR")
	case tea.KeyF4:
		return []byte("\x1bOS")
	case tea.KeyF5:
		return []byte("\x1b[15~")
	case tea.KeyF6:
		return []byte("\x1b[17~")
	case tea.KeyF7:
		return []byte("\x1b[18~")
	case tea.KeyF8:
		return []byte("\x1b[19~")
	case tea.KeyF9:
		return []byte("\x1b[20~")
	case tea.KeyF10:
		return []byte("\x1b[21~")
	case tea.KeyF11:
		return []byte("\x1b[23~")
	case tea.KeyF12:
		return []byte("\x1b[24~")
	default:
		return nil
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

	// Initialize and run the TUI
	m := initialModel(client)
	p := tea.NewProgram(m, tea.WithAltScreen())

	if err := p.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
		os.Exit(1)
	}

	// Application runs entirely within the TUI now
	// SSH connections are handled within the application
}
