package latitude

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	latitudeshsdk "github.com/latitudesh/latitudesh-go-sdk"
	"github.com/latitudesh/latitudesh-go-sdk/models/operations"
)

type Client struct {
	sdk *latitudeshsdk.Latitudesh
}

type ServerSpec struct {
	Project         string
	Plan            string
	OperatingSystem string
	Site            string
	Hostname        string
	SSHKeys         []string
	UserData        string
}

type Server struct {
	ID        string
	Status    string
	Hostname  string
	IPAddress []string
}

// VLAN represents a Latitude.sh Virtual Network (VLAN)
type VLAN struct {
	ID      string
	VID     int
	Subnet  string
	Project string
}

// CreateVLANRequest defines the parameters for creating a VLAN
type CreateVLANRequest struct {
	ProjectID string
	Site      string
	Subnet    string
	VID       *int
}

// ClientInterface defines the methods for interacting with Latitude.sh API
type ClientInterface interface {
	CreateServer(ctx context.Context, spec ServerSpec) (*Server, error)
	GetServer(ctx context.Context, serverID string) (*Server, error)
	DeleteServer(ctx context.Context, serverID string) error
	WaitForServer(ctx context.Context, serverID string, targetStatus string, timeout time.Duration) (*Server, error)
	GetAvailablePlans(ctx context.Context) ([]string, error)
	GetAvailableRegions(ctx context.Context) ([]string, error)
	CreateUserData(ctx context.Context, req CreateUserDataRequest) (string, error)
	DeleteUserData(ctx context.Context, userDataID string) error

	// VLAN management
	CreateVLAN(ctx context.Context, req CreateVLANRequest) (*VLAN, error)
	GetVLAN(ctx context.Context, vlanID string) (*VLAN, error)
	ListVLANs(ctx context.Context, projectID string) ([]*VLAN, error)
	DeleteVLAN(ctx context.Context, vlanID string) error
	AttachServerToVLAN(ctx context.Context, serverID, vlanID string) error
	DetachServerFromVLAN(ctx context.Context, serverID, vlanID string) error
}

var _ ClientInterface = (*Client)(nil)

func NewClient() (*Client, error) {
	apiKey := os.Getenv("LATITUDE_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("LATITUDE_API_KEY environment variable is required")
	}

	sdk := latitudeshsdk.New(
		latitudeshsdk.WithSecurity(apiKey),
		latitudeshsdk.WithTimeout(60*time.Second),
	)

	return &Client{
		sdk: sdk,
	}, nil
}

type CreateUserDataRequest struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type CreateUserDataResponse struct {
	ID string `json:"id"`
}

func (c *Client) CreateUserData(ctx context.Context, req CreateUserDataRequest) (string, error) {
	var resp CreateUserDataResponse

	res, err := c.sdk.UserData.CreateNew(ctx, operations.PostUserDataUserDataRequestBody{
		Data: operations.PostUserDataUserDataData{
			Type: operations.PostUserDataUserDataTypeUserData,
			Attributes: &operations.PostUserDataUserDataAttributes{
				Description: req.Name,
				Content:     req.Content,
			},
		},
	})

	if err != nil {
		return "", err
	}
	if res.UserData != nil {
		resp.ID = *res.UserData.Data.ID
		return resp.ID, nil
	}

	return "", nil
}

func (c *Client) DeleteUserData(ctx context.Context, userDataID string) error {
	_, err := c.sdk.UserData.Delete(ctx, userDataID)
	if err != nil {
		return fmt.Errorf("failed to delete user data: %w", err)
	}

	return nil
}

func (c *Client) CreateServer(ctx context.Context, spec ServerSpec) (*Server, error) {
	// Validate server spec
	if err := c.validateServerSpec(spec); err != nil {
		return nil, fmt.Errorf("invalid server spec: %w", err)
	}

	// Build create request according to Latitude.sh API spec
	attrs := &operations.CreateServerServersAttributes{
		Project:         &spec.Project,
		Plan:            (*operations.CreateServerPlan)(&spec.Plan),
		OperatingSystem: (*operations.CreateServerOperatingSystem)(&spec.OperatingSystem),
		Site:            (*operations.CreateServerSite)(&spec.Site),
		Hostname:        &spec.Hostname,
		SSHKeys:         spec.SSHKeys,
	}

	// Set billing to monthly as default
	billing := operations.CreateServerBillingMonthly
	attrs.Billing = &billing

	// Add user data if provided
	if spec.UserData != "" {
		attrs.UserData = &spec.UserData
	}

	// Create server
	createRequest := operations.CreateServerServersRequestBody{
		Data: &operations.CreateServerServersData{
			Type:       operations.CreateServerServersTypeServers,
			Attributes: attrs,
		},
	}

	// Send request
	result, err := c.sdk.Servers.Create(ctx, createRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to create server: %w", err)
	}

	if result.Server == nil || result.Server.Data == nil || result.Server.Data.Attributes == nil {
		return nil, fmt.Errorf("invalid response from server creation")
	}

	server := &Server{
		ID:       *result.Server.Data.ID,
		Hostname: spec.Hostname,
	}

	// Get status if available
	if result.Server.Data.Attributes != nil && result.Server.Data.Attributes.Status != nil {
		server.Status = string(*result.Server.Data.Attributes.Status)
	}

	return server, nil
}

// GetServer retrieves server information by ID
func (c *Client) GetServer(ctx context.Context, serverID string) (*Server, error) {
	// Get server details (nil for project ID means get from any project)
	result, err := c.sdk.Servers.Get(ctx, serverID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}

	if result.Server == nil || result.Server.Data == nil {
		return nil, fmt.Errorf("server %s not found", serverID)
	}

	server := &Server{
		ID: serverID,
	}

	attrs := result.Server.Data.Attributes
	if attrs != nil {
		if attrs.Hostname != nil {
			server.Hostname = *attrs.Hostname
		}
		if attrs.Status != nil {
			server.Status = string(*attrs.Status)
		}
	}

	if attrs.PrimaryIpv4 != nil {
		server.IPAddress = append(server.IPAddress, *attrs.PrimaryIpv4)
	}

	return server, nil
}

func (c *Client) DeleteServer(ctx context.Context, serverID string) error {
	// Delete server
	_, err := c.sdk.Servers.Delete(ctx, serverID, nil)
	if err != nil {
		return fmt.Errorf("failed to delete server: %w", err)
	}

	return nil
}

func (c *Client) WaitForServer(ctx context.Context, serverID string, targetStatus string, timeout time.Duration) (*Server, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout waiting for server %s to reach %s", serverID, targetStatus)
		case <-ticker.C:
			server, err := c.GetServer(ctx, serverID)
			if err != nil {
				return nil, fmt.Errorf("failed to get server: %w", err)
			}

			if strings.EqualFold(server.Status, targetStatus) {
				return server, nil
			}
		}
	}
}

// GetAvailablePlans retrieves available server plans
func (c *Client) GetAvailablePlans(ctx context.Context) ([]string, error) {
	// Get available plans
	result, err := c.sdk.Plans.List(ctx, operations.GetPlansRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get available plans: %w", err)
	}

	var plans []string
	if result.Object != nil && result.Object.Data != nil {
		for _, plan := range result.Object.Data {
			if plan.Attributes != nil && plan.Attributes.Slug != nil {
				plans = append(plans, *plan.Attributes.Slug)
			}
		}
	}

	return plans, nil
}

// GetAvailableRegions retrieves available regions
func (c *Client) GetAvailableRegions(ctx context.Context) ([]string, error) {
	// Get available regions
	result, err := c.sdk.Regions.Get(ctx, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get available regions: %w", err)
	}

	var regions []string
	if result.Regions != nil && result.Regions.Data != nil {
		for _, region := range result.Regions.Data {
			if region.Attributes != nil && region.Attributes.Slug != nil {
				regions = append(regions, *region.Attributes.Slug)
			}
		}
	}

	return regions, nil
}

func (c *Client) validateServerSpec(spec ServerSpec) error {
	if spec.Project == "" {
		return fmt.Errorf("project is required")
	}
	if spec.Plan == "" {
		return fmt.Errorf("plan is required")
	}
	if spec.OperatingSystem == "" {
		return fmt.Errorf("operatingSystem is required")
	}
	if spec.Site == "" {
		return fmt.Errorf("site is required")
	}
	return nil
}

// CreateVLAN creates a new VLAN (Virtual Network) in the specified project
// If a VLAN with the same description already exists, returns the existing VLAN instead of creating a new one
func (c *Client) CreateVLAN(ctx context.Context, req CreateVLANRequest) (*VLAN, error) {
	if req.ProjectID == "" {
		return nil, fmt.Errorf("projectID is required")
	}
	if req.Subnet == "" {
		return nil, fmt.Errorf("subnet is required")
	}
	if req.Site == "" {
		return nil, fmt.Errorf("site is required")
	}

	// Check if a VLAN with the same description already exists (idempotency)
	existingVLANs, err := c.ListVLANs(ctx, req.ProjectID)
	if err != nil {
		// Log but don't fail - we'll try to create anyway
		fmt.Printf("Warning: failed to list existing VLANs: %v\n", err)
	} else {
		for _, existingVLAN := range existingVLANs {
			if existingVLAN.Subnet == req.Subnet {
				// Found existing VLAN with same description - return it instead of creating new one
				fmt.Printf("Found existing VLAN with description %s, reusing ID: %s\n", req.Subnet, existingVLAN.ID)
				return existingVLAN, nil
			}
		}
	}

	// No existing VLAN found, create a new one
	// Convert site to lowercase for API compatibility
	siteValue := strings.ToLower(req.Site)
	site := operations.CreateVirtualNetworkPrivateNetworksSite(siteValue)
	createReq := operations.CreateVirtualNetworkPrivateNetworksRequestBody{
		Data: operations.CreateVirtualNetworkPrivateNetworksData{
			Type: operations.CreateVirtualNetworkPrivateNetworksTypeVirtualNetwork,
			Attributes: operations.CreateVirtualNetworkPrivateNetworksAttributes{
				Project:     req.ProjectID,
				Site:        &site,
				Description: req.Subnet,
			},
		},
	}

	res, err := c.sdk.PrivateNetworks.Create(ctx, createReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create VLAN: %w", err)
	}

	if res.VirtualNetwork == nil || res.VirtualNetwork.Data == nil {
		return nil, fmt.Errorf("empty response from VLAN creation")
	}

	vlanData := res.VirtualNetwork.Data
	vlan := &VLAN{
		ID:      *vlanData.ID,
		Project: req.ProjectID,
		Subnet:  req.Subnet,
	}

	// Parse VID from attributes if available
	if vlanData.Attributes != nil && vlanData.Attributes.Vid != nil {
		vlan.VID = int(*vlanData.Attributes.Vid)
	}

	return vlan, nil
}

// GetVLAN retrieves VLAN information by ID
func (c *Client) GetVLAN(ctx context.Context, vlanID string) (*VLAN, error) {
	if vlanID == "" {
		return nil, fmt.Errorf("vlanID is required")
	}

	res, err := c.sdk.PrivateNetworks.Get(ctx, vlanID)
	if err != nil {
		return nil, fmt.Errorf("failed to get VLAN: %w", err)
	}

	if res.Object == nil || res.Object.Data == nil || res.Object.Data.Data == nil {
		return nil, fmt.Errorf("VLAN not found: %s", vlanID)
	}

	vlanData := res.Object.Data.Data
	vlan := &VLAN{
		ID: *vlanData.ID,
	}

	if vlanData.Attributes != nil {
		if vlanData.Attributes.Description != nil {
			vlan.Subnet = *vlanData.Attributes.Description
		}
		if vlanData.Attributes.Vid != nil {
			vlan.VID = int(*vlanData.Attributes.Vid)
		}
	}

	return vlan, nil
}

// ListVLANs lists all VLANs accessible to the API key
// Note: Returns all VLANs, filtering by description should be done by caller
func (c *Client) ListVLANs(ctx context.Context, projectID string) ([]*VLAN, error) {
	// List all virtual networks using PrivateNetworks.List with proper request
	req := operations.GetVirtualNetworksRequest{}
	res, err := c.sdk.PrivateNetworks.List(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list VLANs: %w", err)
	}

	var vlans []*VLAN
	if res.VirtualNetworks != nil && res.VirtualNetworks.Data != nil {
		for _, vlanData := range res.VirtualNetworks.Data {
			if vlanData.Attributes == nil {
				continue
			}

			vlan := &VLAN{
				ID:      *vlanData.ID,
				Project: projectID, // Set to requested project ID
			}

			if vlanData.Attributes.Description != nil {
				vlan.Subnet = *vlanData.Attributes.Description
			}
			if vlanData.Attributes.Vid != nil {
				vlan.VID = int(*vlanData.Attributes.Vid)
			}

			vlans = append(vlans, vlan)
		}
	}

	return vlans, nil
}

// DeleteVLAN deletes a VLAN by ID
func (c *Client) DeleteVLAN(ctx context.Context, vlanID string) error {
	if vlanID == "" {
		return fmt.Errorf("vlanID is required")
	}

	_, err := c.sdk.VirtualNetworks.Delete(ctx, vlanID)
	if err != nil {
		// Check if already deleted
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "404") {
			return nil
		}
		return fmt.Errorf("failed to delete VLAN: %w", err)
	}

	return nil
}

// AttachServerToVLAN attaches a server to a VLAN
func (c *Client) AttachServerToVLAN(ctx context.Context, serverID, vlanID string) error {
	if serverID == "" {
		return fmt.Errorf("serverID is required")
	}
	if vlanID == "" {
		return fmt.Errorf("vlanID is required")
	}

	assignReq := operations.AssignServerVirtualNetworkPrivateNetworksRequestBody{
		Data: &operations.AssignServerVirtualNetworkPrivateNetworksData{
			Type: operations.AssignServerVirtualNetworkPrivateNetworksTypeVirtualNetworkAssignment,
			Attributes: &operations.AssignServerVirtualNetworkPrivateNetworksAttributes{
				ServerID:         serverID,
				VirtualNetworkID: vlanID,
			},
		},
	}

	_, err := c.sdk.PrivateNetworks.Assign(ctx, assignReq)
	if err != nil {
		// Check if already assigned
		if strings.Contains(err.Error(), "already assigned") || strings.Contains(err.Error(), "already exists") {
			return nil
		}
		return fmt.Errorf("failed to attach server to VLAN: %w", err)
	}

	return nil
}

// DetachServerFromVLAN detaches a server from a VLAN
func (c *Client) DetachServerFromVLAN(ctx context.Context, serverID, vlanID string) error {
	if serverID == "" {
		return fmt.Errorf("serverID is required")
	}
	if vlanID == "" {
		return fmt.Errorf("vlanID is required")
	}

	// Note: The assignment ID format may need to be adjusted based on API behavior
	assignmentID := fmt.Sprintf("%s-%s", serverID, vlanID)

	_, err := c.sdk.PrivateNetworks.DeleteAssignment(ctx, assignmentID)
	if err != nil {
		// Check if already detached
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "404") {
			return nil
		}
		return fmt.Errorf("failed to detach server from VLAN: %w", err)
	}

	return nil
}
