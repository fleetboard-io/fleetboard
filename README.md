# ovn-builder

### how to deploy this
1. Label the `target node` you want OVN-central to be attached.
   ```bash
       kubectl label node -lnode-role.kubernetes.io/control-plane kube-ovn/role=master --overwrite
   ```
2. Get the node ip of the `target Node`.
   controller_node_ip=`kubectl get node -o wide --no-headers | grep -E "control-plane|bpf1" | awk -F " " '{print $6}'`
3. Change the default ENV of deploy files.
   NODE_IPS in central-deploy.yaml and ovn-controller.yaml should be $controller_node_ip.
   OVN_DB_IPS in ovsovn-ds.yaml should also be $controller_node_ip.
4. Then deploy.
   ```
    kubectl create -f deploy/templates
   ```