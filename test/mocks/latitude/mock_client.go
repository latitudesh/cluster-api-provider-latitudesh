package latitude

import (
	"context"
	"time"

	"github.com/latitudesh/cluster-api-provider-latitudesh/internal/latitude"
)

// MockClient is a mock implementation of the Latitude client for testing
type MockClient struct {
	CreateServerFunc  func(ctx context.Context, spec latitude.ServerSpec) (*latitude.Server, error)
	GetServerFunc     func(ctx context.Context, serverID string) (*latitude.Server, error)
	DeleteServerFunc  func(ctx context.Context, serverID string) error
	WaitForServerFunc func(ctx context.Context, serverID string, targetStatus string,
		timeout time.Duration) (*latitude.Server, error)
	GetAvailablePlansFunc   func(ctx context.Context) ([]string, error)
	GetAvailableRegionsFunc func(ctx context.Context) ([]string, error)
	CreateUserDataFunc      func(ctx context.Context, req latitude.CreateUserDataRequest) (string, error)
	DeleteUserDataFunc      func(ctx context.Context, userDataID string) error

	// VLAN management
	CreateVLANFunc           func(ctx context.Context, req latitude.CreateVLANRequest) (*latitude.VLAN, error)
	GetVLANFunc              func(ctx context.Context, vlanID string) (*latitude.VLAN, error)
	ListVLANsFunc            func(ctx context.Context, projectID string) ([]*latitude.VLAN, error)
	DeleteVLANFunc           func(ctx context.Context, vlanID string) error
	AttachServerToVLANFunc   func(ctx context.Context, serverID, vlanID string) error
	DetachServerFromVLANFunc func(ctx context.Context, serverID, vlanID string) error
}

func (m *MockClient) CreateServer(ctx context.Context, spec latitude.ServerSpec) (*latitude.Server, error) {
	if m.CreateServerFunc != nil {
		return m.CreateServerFunc(ctx, spec)
	}
	return &latitude.Server{
		ID:        "mock-server-id",
		Status:    "on",
		Hostname:  spec.Hostname,
		IPAddress: []string{"192.168.1.1"},
	}, nil
}

func (m *MockClient) GetServer(ctx context.Context, serverID string) (*latitude.Server, error) {
	if m.GetServerFunc != nil {
		return m.GetServerFunc(ctx, serverID)
	}
	return &latitude.Server{
		ID:        serverID,
		Status:    "on",
		Hostname:  "mock-hostname",
		IPAddress: []string{"192.168.1.1"},
	}, nil
}

func (m *MockClient) DeleteServer(ctx context.Context, serverID string) error {
	if m.DeleteServerFunc != nil {
		return m.DeleteServerFunc(ctx, serverID)
	}
	return nil
}

func (m *MockClient) WaitForServer(
	ctx context.Context,
	serverID string,
	targetStatus string,
	timeout time.Duration,
) (*latitude.Server, error) {
	if m.WaitForServerFunc != nil {
		return m.WaitForServerFunc(ctx, serverID, targetStatus, timeout)
	}
	return &latitude.Server{
		ID:        serverID,
		Status:    targetStatus,
		Hostname:  "mock-hostname",
		IPAddress: []string{"192.168.1.1"},
	}, nil
}

func (m *MockClient) GetAvailablePlans(ctx context.Context) ([]string, error) {
	if m.GetAvailablePlansFunc != nil {
		return m.GetAvailablePlansFunc(ctx)
	}
	return []string{"c3-small-x86", "c3-medium-x86"}, nil
}

func (m *MockClient) GetAvailableRegions(ctx context.Context) ([]string, error) {
	if m.GetAvailableRegionsFunc != nil {
		return m.GetAvailableRegionsFunc(ctx)
	}
	return []string{"SAO", "NYC", "LON"}, nil
}

func (m *MockClient) CreateUserData(ctx context.Context, req latitude.CreateUserDataRequest) (string, error) {
	if m.CreateUserDataFunc != nil {
		return m.CreateUserDataFunc(ctx, req)
	}
	return "mock-userdata-id", nil
}

func (m *MockClient) DeleteUserData(ctx context.Context, userDataID string) error {
	if m.DeleteUserDataFunc != nil {
		return m.DeleteUserDataFunc(ctx, userDataID)
	}
	return nil
}

func (m *MockClient) CreateVLAN(ctx context.Context, req latitude.CreateVLANRequest) (*latitude.VLAN, error) {
	if m.CreateVLANFunc != nil {
		return m.CreateVLANFunc(ctx, req)
	}
	return &latitude.VLAN{
		ID:      "mock-vlan-id",
		VID:     100,
		Subnet:  req.Subnet,
		Project: req.ProjectID,
	}, nil
}

func (m *MockClient) GetVLAN(ctx context.Context, vlanID string) (*latitude.VLAN, error) {
	if m.GetVLANFunc != nil {
		return m.GetVLANFunc(ctx, vlanID)
	}
	return &latitude.VLAN{
		ID:      vlanID,
		VID:     100,
		Subnet:  "10.8.0.0/24",
		Project: "mock-project-id",
	}, nil
}

func (m *MockClient) ListVLANs(ctx context.Context, projectID string) ([]*latitude.VLAN, error) {
	if m.ListVLANsFunc != nil {
		return m.ListVLANsFunc(ctx, projectID)
	}
	return []*latitude.VLAN{}, nil
}

func (m *MockClient) DeleteVLAN(ctx context.Context, vlanID string) error {
	if m.DeleteVLANFunc != nil {
		return m.DeleteVLANFunc(ctx, vlanID)
	}
	return nil
}

func (m *MockClient) AttachServerToVLAN(ctx context.Context, serverID, vlanID string) error {
	if m.AttachServerToVLANFunc != nil {
		return m.AttachServerToVLANFunc(ctx, serverID, vlanID)
	}
	return nil
}

func (m *MockClient) DetachServerFromVLAN(ctx context.Context, serverID, vlanID string) error {
	if m.DetachServerFromVLANFunc != nil {
		return m.DetachServerFromVLANFunc(ctx, serverID, vlanID)
	}
	return nil
}
