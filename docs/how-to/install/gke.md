# Install on Google GKE

This guide walks you from a working GKE cluster to a running kagents operator backed by Google Filestore for the ReadWriteMany storage requirement.

## Prerequisites

- A GKE cluster on Kubernetes 1.28+
- `kubectl` configured against the cluster
- `helm` 3.14+
- `gcloud` CLI authenticated with the project that owns the cluster
- The cluster's VPC network and region. `gcloud container clusters describe <cluster-name>` shows them

## 1. Enable the Filestore CSI driver

GKE provides the Filestore CSI driver as a managed add-on. Enable it on the cluster:

```bash
gcloud container clusters update <cluster-name> \
  --update-addons=GcpFilestoreCsiDriver=ENABLED \
  --location <region-or-zone>
```

For new clusters you can enable it at create time with `--addons=GcpFilestoreCsiDriver`.

Verify the driver pods are running:

```bash
kubectl get pods -n kube-system -l k8s-app=gcp-filestore-csi-driver
```

## 2. Create the StorageClass

The driver supports dynamic provisioning, so you don't need to create a Filestore instance manually. The CSI driver creates one when the first PVC binds.

```yaml title="storageclass-filestore.yaml"
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  # Match the operator's default StorageClass name.
  name: nfs
provisioner: filestore.csi.storage.gke.io
parameters:
  tier: standard         # or "premium" for SSD-backed; "enterprise" for regional HA
  network: default       # match your cluster's VPC
volumeBindingMode: WaitForFirstConsumer
allowVolumeExpansion: true
reclaimPolicy: Delete
```

Apply it:

```bash
kubectl apply -f storageclass-filestore.yaml
```

`WaitForFirstConsumer` defers provisioning until a pod is scheduled, which lets the Filestore instance land in the right zone for the consuming pod.

## 3. Install kagents

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

## 4. Verify with the mailbox smoke test

```bash
git clone https://github.com/amcheste/kagents.git
cd kagents
make mailbox-smoke-test
```

A passing run reports the effective StorageClass and AccessMode:

```
PASS  StorageClass=nfs  AccessMode=ReadWriteMany  RoundTripMs=623
```

The first `make mailbox-smoke-test` run on Filestore takes a few minutes. Filestore instance provisioning is the slow step (~3-5 min). Subsequent test runs reuse the instance and complete in under 30s.

## Cost notes

Filestore is billed by provisioned capacity per hour, not actual usage:

- **Standard tier**: ~$0.20/GiB-month. Minimum instance size is **1 TiB**, so the floor is ~$200/month per Filestore instance.
- **Premium tier (SSD)**: ~$0.30/GiB-month. Same 1 TiB minimum.
- **Enterprise tier (HA, regional)**: ~$0.60/GiB-month. 2.5 TiB minimum.

Note that **each PVC creates a new Filestore instance by default** with this StorageClass config. If you're running many teams, this gets expensive fast. At least one instance per PVC times the 1 TiB minimum.

For multi-team production use, set `volumeHandle` on a manually-provisioned shared Filestore instance and use sub-directory provisioning instead. See [GKE's Filestore docs](https://cloud.google.com/kubernetes-engine/docs/how-to/persistent-volumes/filestore-csi-driver) for the multi-PVC pattern.

The honest range for a small production install with one shared Filestore instance is **$200–$300/month**.

## Common gotchas

??? warning "PVC stuck in `Pending` with `does not satisfy capacity`"
    Filestore instances have a 1 TiB minimum size. The kagents chart's default `storage.teamStateSize` is `5Gi`, but Filestore will round it up to the tier minimum. The PVC binds successfully. The warning resolves once provisioning completes (3-5 min).

??? warning "`failed to create filestore instance: insufficient quota`"
    Filestore instances count against a project-wide quota. `gcloud compute regions describe <region>` shows current usage. Request a quota increase via the GCP console.

??? warning "Pods can't reach the Filestore IP"
    The Filestore instance must be in the same VPC as the cluster. The StorageClass `network: default` parameter must match your cluster's VPC name. If you use a custom VPC, set it explicitly.

??? warning "`Failed to create access mode RWO from RWX SC`"
    Don't use `--pvc-access-mode=ReadWriteOnce` on GKE. Filestore is RWX-native; the operator just needs the default `ReadWriteMany` to work.

## Where to look next

- [Resource model](../../explanation/resources.md). The CRDs you'll be writing
- [Coordination protocol](../../explanation/coordination.md). Why RWX matters in detail
- [Operations](../../explanation/operations.md). Budget, RBAC, observability for the running operator
