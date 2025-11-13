package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CloudflareLoadBalancerSpec defines the desired state of CloudflareLoadBalancer
type CloudflareLoadBalancerSpec struct {
	// AccountID is the Cloudflare Account ID (required for load balancer pools)
	AccountID string `json:"accountId"`

	// ZoneID is the Cloudflare Zone ID where the load balancer will be created
	ZoneID string `json:"zoneId"`

	// Hostname is the DNS name for the load balancer (e.g., "api.mycluster.example.com")
	Hostname string `json:"hostname"`

	// Port is the port that the load balancer will listen on (default: 6443 for K8s API)
	// +kubebuilder:default=6443
	// +optional
	Port int32 `json:"port,omitempty"`

	// CredentialsRef references the Secret containing Cloudflare API credentials
	CredentialsRef *CloudflareCredentialsRef `json:"credentialsRef"`

	// ClusterRef references the LatitudeCluster this load balancer serves
	ClusterRef CloudflareClusterRef `json:"clusterRef"`

	// HealthCheckPath is the path used for health checks (default: /healthz)
	// +kubebuilder:default="/healthz"
	// +optional
	HealthCheckPath string `json:"healthCheckPath,omitempty"`

	// HealthCheckInterval is the interval between health checks in seconds (default: 60)
	// +kubebuilder:default=60
	// +optional
	HealthCheckInterval int32 `json:"healthCheckInterval,omitempty"`

	// TTL is the DNS TTL in seconds (default: 120)
	// +kubebuilder:default=120
	// +optional
	TTL int32 `json:"ttl,omitempty"`

	// Proxied indicates if traffic should be proxied through Cloudflare (default: false for K8s API)
	// +kubebuilder:default=false
	// +optional
	Proxied bool `json:"proxied,omitempty"`
}

// CloudflareCredentialsRef references a Secret containing Cloudflare API credentials
type CloudflareCredentialsRef struct {
	// Name is the name of the Secret containing Cloudflare credentials
	Name string `json:"name"`

	// Namespace is the namespace of the Secret (defaults to the same namespace as CloudflareLoadBalancer)
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// CloudflareClusterRef references a LatitudeCluster
type CloudflareClusterRef struct {
	// Name is the name of the LatitudeCluster
	Name string `json:"name"`

	// Namespace is the namespace of the LatitudeCluster (defaults to the same namespace)
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// OriginStatus represents the status of a single origin (control plane node)
type OriginStatus struct {
	// Name is the name of the origin (typically the machine name)
	Name string `json:"name"`

	// Address is the IP address of the origin
	Address string `json:"address"`

	// Healthy indicates if the origin is passing health checks
	Healthy bool `json:"healthy"`
}

// CloudflareLoadBalancerStatus defines the observed state of CloudflareLoadBalancer
type CloudflareLoadBalancerStatus struct {
	// Ready indicates that the load balancer is fully configured and operational
	Ready bool `json:"ready,omitempty"`

	// LoadBalancerID is the Cloudflare Load Balancer ID
	// +optional
	LoadBalancerID string `json:"loadBalancerId,omitempty"`

	// PoolID is the Cloudflare Pool ID containing the origins
	// +optional
	PoolID string `json:"poolId,omitempty"`

	// DNSRecordID is the Cloudflare DNS record ID
	// +optional
	DNSRecordID string `json:"dnsRecordId,omitempty"`

	// Origins is the list of control plane origins registered with the load balancer
	// +optional
	Origins []OriginStatus `json:"origins,omitempty"`

	// Endpoint is the fully qualified domain name of the load balancer
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// Conditions defines current service state of the CloudflareLoadBalancer
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=cloudflareloadbalancers,scope=Namespaced,shortName=cflb
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="Hostname",type="string",JSONPath=".spec.hostname"
// +kubebuilder:printcolumn:name="Endpoint",type="string",JSONPath=".status.endpoint"
// CloudflareLoadBalancer is the Schema for the cloudflareloadbalancers API
type CloudflareLoadBalancer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CloudflareLoadBalancerSpec   `json:"spec,omitempty"`
	Status CloudflareLoadBalancerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// CloudflareLoadBalancerList contains a list of CloudflareLoadBalancer
type CloudflareLoadBalancerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CloudflareLoadBalancer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CloudflareLoadBalancer{}, &CloudflareLoadBalancerList{})
}

// GetConditions returns the set of conditions for this object
func (c *CloudflareLoadBalancer) GetConditions() []metav1.Condition {
	return c.Status.Conditions
}

// SetConditions sets the conditions for this object
func (c *CloudflareLoadBalancer) SetConditions(conditions []metav1.Condition) {
	c.Status.Conditions = conditions
}
