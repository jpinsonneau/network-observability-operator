/*
Copyright 2021.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type MetricType string
type FilterMatchType string
type FlowDirection string

const (
	CounterMetric   MetricType = "Counter"
	HistogramMetric MetricType = "Histogram"
	// Note: we don't expose gauge on purpose to avoid configuration mistake related to gauge limitation.
	// 99% of times, "counter" or "histogram" should be the ones to use. We can eventually revisit later.
	MatchEqual    FilterMatchType = "Equal"
	MatchNotEqual FilterMatchType = "NotEqual"
	MatchPresence FilterMatchType = "Presence"
	MatchAbsence  FilterMatchType = "Absence"
	MatchRegex    FilterMatchType = "MatchRegex"
	MatchNotRegex FilterMatchType = "NotMatchRegex"
	Egress        FlowDirection   = "Egress"
	Ingress       FlowDirection   = "Ingress"
	AnyDirection  FlowDirection   = "Any"
)

type MetricFilter struct {
	// Name of the field to filter on
	// +required
	Field string `json:"field"`

	// Value to filter on
	// +optional
	Value string `json:"value"`

	// Type of matching to apply
	// +kubebuilder:validation:Enum:="Equal";"NotEqual";"Presence";"Absence";"MatchRegex";"NotMatchRegex"
	// +kubebuilder:default:="Equal"
	MatchType FilterMatchType `json:"matchType"`
}

// FlowMetricSpec defines the desired state of FlowMetric
// The provided API allows you to customize these metrics according to your needs.<br>
// When adding new metrics or modifying existing labels, you must carefully monitor the memory
// usage of Prometheus workloads as this could potentially have a high impact. Cf https://rhobs-handbook.netlify.app/products/openshiftmonitoring/telemetry.md/#what-is-the-cardinality-of-a-metric<br>
// To check the cardinality of all NetObserv metrics, run as `promql`: `count({__name__=~"netobserv.*"}) by (__name__)`.
type FlowMetricSpec struct {
	// Name of the metric in Prometheus. It will be automatically prefixed with "netobserv_".
	// +required
	MetricName string `json:"metricName"`

	// Metric type: "Counter" or "Histogram".
	// Use "Counter" for any value that increases over time and on which you can compute a rate, such as Bytes or Packets.
	// Use "Histogram" for any value that must be sampled independently, such as latencies.
	// +kubebuilder:validation:Enum:="Counter";"Histogram"
	// +required
	Type MetricType `json:"type"`

	// `valueField` is the flow field that must be used as a value for this metric. This field must hold numeric values.
	// Leave empty to count flows rather than a specific value per flow.
	// Refer to the documentation for the list of available fields: https://docs.openshift.com/container-platform/latest/networking/network_observability/json-flows-format-reference.html.
	// +optional
	ValueField string `json:"valueField,omitempty"`

	// `filters` is a list of fields and values used to restrict which flows are taken into account. Oftentimes, these filters must
	// be used to eliminate duplicates: `Duplicate != "true"` and `FlowDirection = "0"`.
	// Refer to the documentation for the list of available fields: https://docs.openshift.com/container-platform/latest/networking/network_observability/json-flows-format-reference.html.
	// +optional
	Filters []MetricFilter `json:"filters"`

	// `labels` is a list of fields that should be used as Prometheus labels, also known as dimensions.
	// From choosing labels results the level of granularity of this metric, as well as the available aggregations at query time.
	// It must be done carefully as it impacts the metric cardinality (cf https://rhobs-handbook.netlify.app/products/openshiftmonitoring/telemetry.md/#what-is-the-cardinality-of-a-metric).
	// In general, avoid setting very high cardinality labels such as IP or MAC addresses.
	// "SrcK8S_OwnerName" or "DstK8S_OwnerName" should be preferred over "SrcK8S_Name" or "DstK8S_Name" as much as possible.
	// Refer to the documentation for the list of available fields: https://docs.openshift.com/container-platform/latest/network_observability/json-flows-format-reference.html.
	// +optional
	Labels []string `json:"labels"`

	// When set to `true`, flows duplicated across several interfaces will add up in the generated metrics.
	// When set to `false` (default), it is equivalent to adding the exact filter on `Duplicate` != `true`.
	// +optional
	IncludeDuplicates bool `json:"includeDuplicates,omitempty"`

	// Filter for ingress, egress or any direction flows.
	// When set to `Ingress`, it is equivalent to adding the regex filter on `FlowDirection`: `0|2`.
	// When set to `Egress`, it is equivalent to adding the regex filter on `FlowDirection`: `1|2`.
	// +kubebuilder:validation:Enum:="Any";"Egress";"Ingress"
	// +kubebuilder:default:="Any"
	// +optional
	Direction FlowDirection `json:"direction,omitempty"`

	// A list of buckets to use when `type` is "Histogram". The list must be parseable as floats. Prometheus default buckets will be used if unset.
	// +optional
	Buckets []string `json:"buckets,omitempty"`
}

// FlowMetricStatus defines the observed state of FlowMetric
type FlowMetricStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// FlowMetric is the Schema for the flowmetrics API
type FlowMetric struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FlowMetricSpec   `json:"spec,omitempty"`
	Status FlowMetricStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// FlowMetricList contains a list of FlowMetric
type FlowMetricList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FlowMetric `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FlowMetric{}, &FlowMetricList{})
}
