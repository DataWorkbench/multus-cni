## 使用说明


1. `multus-cni`需要有在云平台上操作网络的权限，所以首先需要增加 IaaS 的 sdk 配置文件，并将其存储中kube-system中的`qcsecret`中。

    ```bash
    cat >config.yaml <<EOF
    qy_access_key_id: "Your access key id"
    qy_secret_access_key: "Your secret access key"
    # your instance zone
    zone: "pek3a"
    EOF

    ## 创建Secret
    kubectl create secret generic qcsecret --from-file=./config.yaml -n kube-system
    ```
   access_key 以及 secret_access_key 可以登录青云控制台，在 **API 秘钥**菜单下申请。  请参考https://docs.qingcloud.com/product/api/common/overview.html。默认是配置文件指向青云公网api server，如果是私有云，请按照下方示例配置更多的参数：
    ```
    qy_access_key_id: 'ACCESS_KEY_ID'
    qy_secret_access_key: 'SECRET_ACCESS_KEY'

    host: 'api.xxxxx.com'
    port: 443
    protocol: 'https'
    uri: '/iaas'
    connection_retries: 3
    ```
   上述配置中的host可以配置为api.ks.qingcloud.com，通过内网访问青云api服务
2. 安装yaml文件，等待所有节点的multus起来即可
    ```bash
    kubectl apply -f https://raw.githubusercontent.com/DataWorkbench/multus-cni/dev/deployments/multus-daemonset-qke.yml
    ```