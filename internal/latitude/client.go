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
