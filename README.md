// ========== 环境变量片段 ==========
// 从 Pod metadata 取值
{
    Name: "POD_NAMESPACE",
    ValueFrom: &corev1.EnvVarSource{
        FieldRef: &corev1.ObjectFieldSelector{
            FieldPath: "metadata.namespace",
        },
    },
}

// 从 Secret 取值
{
    Name: "DB_PASSWORD",
    ValueFrom: &corev1.EnvVarSource{
        SecretKeyRef: &corev1.SecretKeySelector{
            LocalObjectReference: corev1.LocalObjectReference{Name: "my-secret"},
            Key: "password",
        },
    },
}


//=======================go常用语法===========================
//循环
for _, env := range envVars {
		if env.Name == "MYSQL_ROOT_PASSWORD" {
			mysqlPassword = env.Value
			break
		}
	}
    
for key, value := range cluster.Spec.Env  //循环字典
for _, pod     := range podList.Items     //循环pod列表
for _, pvc     := range pvcList.Items     //循环pvc列表


//数组和给数组添加内容
//创建数组
envVars := make([]corev1.EnvVars, 0, len(cluster.Spec.Env))

//给数组添加内容
envVars = append(envVars, corev1.EnvVars{Name: "zhangsan", value: "1"})

//循环给envVars添加内容
for k,v :=range cluster.Spec.Env{
    envVars = append(envVars, corev1.EnvVars{Name: k, value: v})
}




//======================k8s常用方法=============================
statefulsetName := cluster.Name + "-statefulset"
statefulsetKey := client.objectKey{namespace: cluster.namespace, Name: statefulsetName}
err := r.Get(ctx, statefulsetKey, &existSts)