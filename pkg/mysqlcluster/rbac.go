package mysqlcluster

import (
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	myappv1 "mysql-operator/api/v1"
)

// 创建role
func NewRbac(cluster *myappv1.MySQLCluster, namespace string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-patcher",
			Namespace: namespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"pods"},
				Verbs:     []string{"patch", "get"},
			},
		},
	}
}

// 2. 定义 RoleBinding，绑定到 default ServiceAccount
func newBingding(cluster *myappv1.MySQLCluster, namespace string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-patcher-binding",
			Namespace: namespace,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "default",
				Namespace: namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			Kind:     "Role",
			Name:     "pod-patcher",
			APIGroup: "rbac.authorization.k8s.io",
		},
	}
}
