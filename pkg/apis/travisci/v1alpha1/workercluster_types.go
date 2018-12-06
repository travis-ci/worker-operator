package v1alpha1

import (
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkerClusterSpec defines the desired state of WorkerCluster
type WorkerClusterSpec struct {
	// Maximum number of concurrent jobs that should be able to be run by this cluster.
	// The operator will attempt to make the total number of jobs across all of the
	// matching worker pods sum to this number.
	MaxJobs int32 `json:"maxJobs"`

	// Label selector for worker pods. It must match the pod template's labels.
	Selector *metav1.LabelSelector `json:"selector"`

	// Template describes the pods that will be created.
	Template v1.PodTemplateSpec `json:"template"`

	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
}

// WorkerClusterStatus defines the observed state of WorkerCluster
type WorkerClusterStatus struct {
	// The status of the processor pools of workers that this cluster is managing.
	WorkerStatuses []WorkerStatus `json:"workerStatuses,omitempty"`

	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
}

// WorkerStatus defines the observed state of a single worker pod in a WorkerCluster
type WorkerStatus struct {
	// The name of the worker pod.
	Name string `json:"name"`

	// The current number of processors running in the worker, as reported by the worker itself.
	CurrentPoolSize int32 `json:"currentPoolSize"`

	// The number of processors the worker expects to be running once any that are gracefully
	// shutting down have finished.
	ExpectedPoolSize int32 `json:"expectedPoolSize"`

	// The number of processors the cluster operator has asked this worker to have. There may
	// delay in bringing processors up or (especially) down, so this may be different than the
	// current pool size. Once fully reconciled, though, the current and requested pool sizes
	// should be equal.
	RequestedPoolSize int32 `json:"requestedPoolSize"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WorkerCluster is the Schema for the workerclusters API
// +k8s:openapi-gen=true
type WorkerCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkerClusterSpec   `json:"spec,omitempty"`
	Status WorkerClusterStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WorkerClusterList contains a list of WorkerCluster
type WorkerClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WorkerCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WorkerCluster{}, &WorkerClusterList{})
}
