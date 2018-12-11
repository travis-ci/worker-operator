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

	// Maximum concurrent jobs that a single worker should be able to run. If MaxJobs exceeds
	// this number, more workers will be started until the number of concurrent jobs per worker
	// is below this number.
	MaxJobsPerWorker int32 `json:"maxJobsPerWorker"`

	// Label selector for worker pods. It must match the pod template's labels.
	Selector *metav1.LabelSelector `json:"selector"`

	// Template describes the workers that will be created.
	Template WorkerTemplateSpec `json:"template"`

	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
}

// WorkerTemplateSpec defines a template for how new worker pods in a cluster will be configured.
type WorkerTemplateSpec struct {
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              WorkerSpec `json:"spec,omitempty"`
}

// WorkerSpec defines the configuration of a worker pod.
type WorkerSpec struct {
	// The container image for running worker. This will usually be a tag of the "travisci/worker"
	// repo from Docker Hub.
	Image string `json:"image,omitempty"`

	// The pull policy for the worker image.
	ImagePullPolicy v1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// A list of environment variables to set on the worker container.
	// The worker cluster will add more variables to this list when creating the deployment.
	Env []v1.EnvVar `json:"env,omitempty"`

	// A list of other sources for environment variables for the worker container.
	EnvFrom []v1.EnvFromSource `json:"env,omitempty"`

	// The name of a secret containing an SSH key for logging into VMs.
	// The secret will be mounted at /etc/worker/ssh in the worker container. Be sure to configure
	// the appropriate environment variable to point the provider at that key.
	SSHKeySecret string `json:"sshKeySecret,omitempty"`
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

	// Current state that the worker is in.
	// It's important that we distinguish which worker pods are running normally and which are
	// in the process of shutting down, for the purposes of assigning pool sizes.
	Phase WorkerPhase `json:"phase"`

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

// WorkerPhase represents the current lifecycle state of a worker.
type WorkerPhase string

const (
	// WorkerPending represents a worker that isn't ready to be assigned a pool size.
	WorkerPending WorkerPhase = "Pending"

	// WorkerRunning represents a worker that is running normally and can have its pool size
	// changed as needed.
	WorkerRunning WorkerPhase = "Running"

	// WorkerTerminating represents a worker that is still running jobs but is in the process
	// of shutting down. Its pool size needs to be accounted for, but it should not be
	// assigned any more processors.
	WorkerTerminating WorkerPhase = "Terminating"
)

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
