# Install on Azure AKS

This guide walks you from a working AKS cluster to a running kagents operator backed by Azure Files for the ReadWriteMany storage requirement.

## Prerequisites

- An AKS cluster on Kubernetes 1.28+
- `kubectl` configured against the cluster
- `helm` 3.14+
- `az` CLI authenticated with the subscription that owns the cluster
- The cluster's resource group and node resource group. `az aks show -g <rg> -n <cluster>` shows them

## 1. Verify the Azure Files CSI driver is enabled

AKS includes the Azure Files CSI driver as a managed add-on, enabled by default on new clusters since 1.21. Verify:

```bash
kubectl get csidriver file.csi.azure.com
```

If the resource doesn't exist, enable it:

```bash
az aks update -g <rg> -n <cluster-name> --enable-file-driver
```

## 2. Choose the file share protocol

Azure Files supports two protocols, and only one is suitable for kagents:

| Protocol | RWX? | Use? |
|----------|------|------|
| **NFS v4.1** | ✅ Yes | **Yes, use this.** |
| **SMB** | ⚠️ Partial | No. POSIX semantics on the agent's mailbox writes don't work reliably. |

NFS shares require a Premium storage account (FileStorage SKU). The good news is Premium pricing is reasonable for the small share sizes kagents needs.

## 3. Create the StorageClass

```yaml title="storageclass-azurefile.yaml"
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  # Match the operator's default StorageClass name.
  name: nfs
provisioner: file.csi.azure.com
parameters:
  protocol: nfs
  skuName: Premium_LRS         # FileStorage SKU; required for NFS shares
  storageAccount: ""           # leave empty for dynamic; populate for an existing account
  resourceGroup: ""            # leave empty to use the AKS node RG
mountOptions:
  - nconnect=4
  - actimeo=30
  - hard
volumeBindingMode: Immediate
allowVolumeExpansion: true
reclaimPolicy: Delete
```

Apply it:

```bash
kubectl apply -f storageclass-azurefile.yaml
```

The `nconnect=4` mount option opens four parallel TCP connections per mount, which significantly improves throughput on Azure Files. `actimeo=30` reduces metadata round-trips for the mailbox-poll workload.

## 4. Install kagents

```bash
helm install kagents \
  oci://ghcr.io/amcheste/charts/kagents \
  --namespace claude-teams-system --create-namespace
```

Wait for the operator:

```bash
kubectl rollout status deployment/kagents-controller-manager \
  --namespace claude-teams-system --timeout=120s
```

## 5. Verify with the mailbox smoke test

```bash
git clone https://github.com/amcheste/kagents.git
cd kagents
make mailbox-smoke-test
```

A passing run reports:

```
PASS  StorageClass=nfs  AccessMode=ReadWriteMany  RoundTripMs=918
```

The first PVC takes longer to provision because the CSI driver creates a storage account if `storageAccount` is empty. Subsequent PVCs in the same RG reuse it.

## Cost notes

Azure Files Premium (FileStorage SKU) is billed by **provisioned capacity** per GiB-month, not actual usage:

- **Provisioned capacity minimum**: 100 GiB per share.
- **Price**: ~$0.16/GiB-month for Premium NFS in most regions, plus tiny per-operation fees.
- **Network**: free within the same Azure region.

A 100 GiB Premium share is **~$16/month**. That's enough for tens of concurrent teams' worth of mailbox state. For larger teams or longer retention, scale capacity up. Azure Files Premium auto-scales IOPS proportional to provisioned size.

The honest range for a small production install is **$15–$50/month** depending on how aggressively you scale capacity for performance.

## Common gotchas

??? warning "`mount.nfs4: Permission denied` or `Stale file handle`"
    The most common cause is the AKS subnet missing the `Microsoft.Storage` service endpoint. Add it: `az network vnet subnet update -g <node-rg> --vnet-name <vnet> -n <subnet> --service-endpoints Microsoft.Storage`.

??? warning "PVCs stuck in `Pending` with `failed to provision volume: ... PrincipalNotFound`"
    The AKS managed identity (or service principal) lacks `Storage Account Contributor` on the resource group. Grant it:
    ```bash
    az role assignment create \
      --assignee <managed-identity-principal-id> \
      --role "Storage Account Contributor" \
      --scope /subscriptions/<sub-id>/resourceGroups/<node-rg>
    ```

??? warning "Slow mailbox round-trips (>5s)"
    Azure Files NFS without `nconnect=4` can be 2-3x slower than expected. Add the mount option in the StorageClass and recreate any pods using existing PVCs to pick it up.

??? warning "Cannot use Standard or Premium_ZRS SKU"
    Only `Premium_LRS` supports NFS. Standard SMB shares technically support RWX but the file-locking semantics don't work for the mailbox protocol. Use Premium NFS.

## Where to look next

- [Resource model](../../explanation/resources.md). The CRDs you'll be writing
- [Coordination protocol](../../explanation/coordination.md). Why RWX matters in detail
- [Operations](../../explanation/operations.md). Budget, RBAC, observability for the running operator
