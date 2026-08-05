package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MySQLClusterSpec defines the desired state of MySQLCluster
type MySQLClusterSpec struct {
	// 副本数（1 主 + N 从）
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=1
	Replicas int32 `json:"replicas,omitempty"`

	// MySQL 镜像
	// +kubebuilder:default="mysql:8.0"
	Image string `json:"image,omitempty"`

	//镜像拉取策略
	ImagePullPolicy corev1.PullPolicy `json:"ImagePullPolicy,omitempty"`

	// 存储大小
	// +kubebuilder:default="10Gi"
	StorageSize string `json:"storageSize,omitempty"`

	// Root 密码（从 Secret 引用）
	RootPasswordSecret string `json:"rootPasswordSecret,omitempty"`

	//端口
	Port int32 `json:"port,omitempty"`

	//环境变量
	Env map[string]string `json:"env,omitempty"`

	//storageclass
	StorageClassName string `json:"StorageClassName,omitempty"`
}

// MySQLClusterStatus defines the observed state of MySQLCluster
type MySQLClusterStatus struct {
	Phase           string   `json:"phase,omitempty"`
	ReadyReplicas   int32    `json:"readyReplicas,omitempty"`
	MasterPod       string   `json:"masterPod,omitempty"`
	SlavePods       []string `json:"slavePods,omitempty"`
	Message         string   `json:"message,omitempty"`
	LastUpdated     string   `json:"lastUpdated,omitempty"`
	StatefulSetName string   `json:"StatefulSetName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Master",type=string,JSONPath=`.status.masterPod`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// MySQLCluster is the Schema for the mysqlclusters API
type MySQLCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`

	Spec MySQLClusterSpec `json:"spec,omitempty"`
	// +optional
	Status MySQLClusterStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// MySQLClusterList contains a list of MySQLCluster
type MySQLClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []MySQLCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MySQLCluster{}, &MySQLClusterList{})
}
