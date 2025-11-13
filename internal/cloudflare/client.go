/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cloudflare

import (
	"context"
	"fmt"

	cloudflare "github.com/cloudflare/cloudflare-go"
)

// Client wraps the Cloudflare API client
type Client struct {
	api *cloudflare.API
}

// NewClient creates a new Cloudflare client
func NewClient(apiToken string) (*Client, error) {
	if apiToken == "" {
		return nil, fmt.Errorf("cloudflare API token is required")
	}

	api, err := cloudflare.NewWithAPIToken(apiToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create cloudflare client: %w", err)
	}

	return &Client{
		api: api,
	}, nil
}

// LoadBalancerPool represents a pool of origins
type LoadBalancerPool struct {
	ID          string
	Name        string
	Description string
	Origins     []Origin
}

// Origin represents a single backend server
type Origin struct {
	Name    string
	Address string
	Enabled bool
	Weight  float64
}

// LoadBalancer represents a Cloudflare Load Balancer
type LoadBalancer struct {
	ID          string
	Name        string
	Description string
	Hostname    string
	Proxied     bool
	TTL         int
	Pools       []string
}

// DNSRecord represents a Cloudflare DNS record
type DNSRecord struct {
	ID      string
	Name    string
	Type    string
	Content string
	Proxied bool
	TTL     int
}

// CreatePool creates a load balancer pool with origins
func (c *Client) CreatePool(ctx context.Context, accountID string, pool LoadBalancerPool) (*LoadBalancerPool, error) {
	origins := make([]cloudflare.LoadBalancerOrigin, len(pool.Origins))
	for i, origin := range pool.Origins {
		origins[i] = cloudflare.LoadBalancerOrigin{
			Name:    origin.Name,
			Address: origin.Address,
			Enabled: origin.Enabled,
			Weight:  origin.Weight,
		}
	}

	lbPool := cloudflare.LoadBalancerPool{
		Name:        pool.Name,
		Description: pool.Description,
		Origins:     origins,
	}

	params := cloudflare.CreateLoadBalancerPoolParams{
		LoadBalancerPool: lbPool,
	}

	rc := cloudflare.AccountIdentifier(accountID)
	result, err := c.api.CreateLoadBalancerPool(ctx, rc, params)
	if err != nil {
		return nil, fmt.Errorf("failed to create pool: %w", err)
	}

	return &LoadBalancerPool{
		ID:          result.ID,
		Name:        result.Name,
		Description: result.Description,
		Origins:     pool.Origins,
	}, nil
}

// UpdatePool updates a load balancer pool with new origins
func (c *Client) UpdatePool(ctx context.Context, accountID, poolID string, pool LoadBalancerPool) (*LoadBalancerPool, error) {
	origins := make([]cloudflare.LoadBalancerOrigin, len(pool.Origins))
	for i, origin := range pool.Origins {
		origins[i] = cloudflare.LoadBalancerOrigin{
			Name:    origin.Name,
			Address: origin.Address,
			Enabled: origin.Enabled,
			Weight:  origin.Weight,
		}
	}

	lbPool := cloudflare.LoadBalancerPool{
		ID:          poolID,
		Name:        pool.Name,
		Description: pool.Description,
		Origins:     origins,
	}

	params := cloudflare.UpdateLoadBalancerPoolParams{
		LoadBalancer: lbPool,
	}

	rc := cloudflare.AccountIdentifier(accountID)
	result, err := c.api.UpdateLoadBalancerPool(ctx, rc, params)
	if err != nil {
		return nil, fmt.Errorf("failed to update pool: %w", err)
	}

	return &LoadBalancerPool{
		ID:          result.ID,
		Name:        result.Name,
		Description: result.Description,
		Origins:     pool.Origins,
	}, nil
}

// GetPool retrieves a load balancer pool
func (c *Client) GetPool(ctx context.Context, accountID, poolID string) (*LoadBalancerPool, error) {
	rc := cloudflare.AccountIdentifier(accountID)
	result, err := c.api.GetLoadBalancerPool(ctx, rc, poolID)
	if err != nil {
		return nil, fmt.Errorf("failed to get pool: %w", err)
	}

	origins := make([]Origin, len(result.Origins))
	for i, origin := range result.Origins {
		origins[i] = Origin{
			Name:    origin.Name,
			Address: origin.Address,
			Enabled: origin.Enabled,
			Weight:  origin.Weight,
		}
	}

	return &LoadBalancerPool{
		ID:          result.ID,
		Name:        result.Name,
		Description: result.Description,
		Origins:     origins,
	}, nil
}

// DeletePool deletes a load balancer pool
func (c *Client) DeletePool(ctx context.Context, accountID, poolID string) error {
	rc := cloudflare.AccountIdentifier(accountID)
	err := c.api.DeleteLoadBalancerPool(ctx, rc, poolID)
	if err != nil {
		return fmt.Errorf("failed to delete pool: %w", err)
	}
	return nil
}

// CreateLoadBalancer creates a load balancer
func (c *Client) CreateLoadBalancer(ctx context.Context, zoneID string, lb LoadBalancer) (*LoadBalancer, error) {
	lbParams := cloudflare.LoadBalancer{
		Name:         lb.Name,
		FallbackPool: lb.Pools[0], // Use first pool as fallback
		DefaultPools: lb.Pools,
		Description:  lb.Description,
		TTL:          lb.TTL,
		Proxied:      lb.Proxied,
	}

	params := cloudflare.CreateLoadBalancerParams{
		LoadBalancer: lbParams,
	}

	rc := cloudflare.ZoneIdentifier(zoneID)
	result, err := c.api.CreateLoadBalancer(ctx, rc, params)
	if err != nil {
		return nil, fmt.Errorf("failed to create load balancer: %w", err)
	}

	return &LoadBalancer{
		ID:          result.ID,
		Name:        result.Name,
		Description: result.Description,
		Proxied:     lb.Proxied,
		TTL:         result.TTL,
		Pools:       result.DefaultPools,
	}, nil
}

// DeleteLoadBalancer deletes a load balancer
func (c *Client) DeleteLoadBalancer(ctx context.Context, zoneID, lbID string) error {
	rc := cloudflare.ZoneIdentifier(zoneID)
	err := c.api.DeleteLoadBalancer(ctx, rc, lbID)
	if err != nil {
		return fmt.Errorf("failed to delete load balancer: %w", err)
	}
	return nil
}

// CreateDNSRecord creates a DNS record
func (c *Client) CreateDNSRecord(ctx context.Context, zoneID string, record DNSRecord) (*DNSRecord, error) {
	proxied := record.Proxied
	params := cloudflare.CreateDNSRecordParams{
		Name:    record.Name,
		Type:    record.Type,
		Content: record.Content,
		Proxied: &proxied,
		TTL:     record.TTL,
	}

	rc := cloudflare.ZoneIdentifier(zoneID)
	result, err := c.api.CreateDNSRecord(ctx, rc, params)
	if err != nil {
		return nil, fmt.Errorf("failed to create DNS record: %w", err)
	}

	resultProxied := false
	if result.Proxied != nil {
		resultProxied = *result.Proxied
	}

	return &DNSRecord{
		ID:      result.ID,
		Name:    result.Name,
		Type:    result.Type,
		Content: result.Content,
		Proxied: resultProxied,
		TTL:     result.TTL,
	}, nil
}

// UpdateDNSRecord updates a DNS record
func (c *Client) UpdateDNSRecord(ctx context.Context, zoneID, recordID string, record DNSRecord) (*DNSRecord, error) {
	proxied := record.Proxied
	params := cloudflare.UpdateDNSRecordParams{
		ID:      recordID,
		Name:    record.Name,
		Type:    record.Type,
		Content: record.Content,
		Proxied: &proxied,
		TTL:     record.TTL,
	}

	rc := cloudflare.ZoneIdentifier(zoneID)
	result, err := c.api.UpdateDNSRecord(ctx, rc, params)
	if err != nil {
		return nil, fmt.Errorf("failed to update DNS record: %w", err)
	}

	resultProxied := false
	if result.Proxied != nil {
		resultProxied = *result.Proxied
	}

	return &DNSRecord{
		ID:      result.ID,
		Name:    result.Name,
		Type:    result.Type,
		Content: result.Content,
		Proxied: resultProxied,
		TTL:     result.TTL,
	}, nil
}

// DeleteDNSRecord deletes a DNS record
func (c *Client) DeleteDNSRecord(ctx context.Context, zoneID, recordID string) error {
	rc := cloudflare.ZoneIdentifier(zoneID)
	err := c.api.DeleteDNSRecord(ctx, rc, recordID)
	if err != nil {
		return fmt.Errorf("failed to delete DNS record: %w", err)
	}
	return nil
}

// GetDNSRecords retrieves DNS records by name
func (c *Client) GetDNSRecords(ctx context.Context, zoneID, name string) ([]DNSRecord, error) {
	params := cloudflare.ListDNSRecordsParams{
		Name: name,
	}

	rc := cloudflare.ZoneIdentifier(zoneID)
	result, _, err := c.api.ListDNSRecords(ctx, rc, params)
	if err != nil {
		return nil, fmt.Errorf("failed to list DNS records: %w", err)
	}

	records := make([]DNSRecord, len(result))
	for i, r := range result {
		proxied := false
		if r.Proxied != nil {
			proxied = *r.Proxied
		}
		records[i] = DNSRecord{
			ID:      r.ID,
			Name:    r.Name,
			Type:    r.Type,
			Content: r.Content,
			Proxied: proxied,
			TTL:     r.TTL,
		}
	}

	return records, nil
}
