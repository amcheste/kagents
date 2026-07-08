# Configure shared storage

kagents needs ReadWriteMany PVCs for the team-state, repo (coding mode), and output (Cowork mode) volumes. This guide covers sizing, backup, and per-backend performance tuning once you've picked a backend.

For the *why* of RWX, see the [Coordination protocol explanation](../../explanation/coordination.md). For initial backend setup, see the cloud-specific install guides ([EKS](../install/eks.md), [GKE](../install/gke.md), [AKS](../install/aks.md)).

## Sizing

The chart's default sizes are conservative; raise them if your teams handle large repos or produce big outputs.

| Volume | Default Helm value | Default size | When to raise |
|--------|-------------------:|-------------:|---------------|
| Team state (mailboxes + tasks) | `storage.teamStateSize` | `5Gi` | Almost never. Mailbox JSON is tiny. 5 GiB holds thousands of messages. |
| Repo (coding mode) | `storage.repoSize` | `20Gi` | If your monorepo + per-teammate worktrees together exceed 20 GiB. Each worktree is roughly the size of your `git checkout`. For a 5-teammate team on a 4 GiB repo, 20 GiB might tip over. |
| Output (Cowork mode) | `spec.workspace.output.size` (per-team) | n/a | Set per AgentTeam based on expected artifact volume. 1 GiB is fine for documents; raise for image/video output. |

Override at install time:

```bash
helm upgrade kagents \
  oci://ghcr.io/amcheste/charts/kagents \
  --namespace claude-teams-system --reuse-values \
  --set storage.teamStateSize=10Gi \
  --set storage.repoSize=50Gi
```

The Cowork output size is per-team, set in the manifest:

```yaml
spec:
  workspace:
    output:
      mountPath: /workspace/output
      size: 5Gi   # adjust per team
```

## Backup

For most use cases the team-state PVC can be discarded. The mailbox is intermediate state, and finished teams' artifacts live elsewhere (in the git remote or in the Cowork output PVC). For the cases where you do want backups:

### EFS (EKS)

Use [AWS Backup](https://aws.amazon.com/backup/) with an EFS resource type. A daily backup with 7-day retention is the standard pattern:

```bash
aws backup create-backup-plan --backup-plan '{
  "BackupPlanName": "kagents-efs-daily",
  "Rules": [{
    "RuleName": "DailyBackup",
    "TargetBackupVaultName": "Default",
    "ScheduleExpression": "cron(0 2 ? * * *)",
    "Lifecycle": {"DeleteAfterDays": 7}
  }]
}'
```

EFS backups are incremental after the first; cost scales with change rate, not full size.

### Filestore (GKE)

Use [Filestore Backups](https://cloud.google.com/filestore/docs/backups). They're snapshot-based; the first is full, subsequent are incremental:

```bash
gcloud filestore backups create kagents-daily-$(date +%Y%m%d) \
  --source-instance <instance-name> \
  --source-instance-region <region> \
  --region <backup-region>
```

Schedule via Cloud Scheduler hitting a Cloud Function that runs the above command.

### Azure Files (AKS)

Premium NFS shares support [Azure Backup](https://learn.microsoft.com/en-us/azure/backup/azure-file-share-backup-overview):

```bash
az backup vault create -g <rg> -n kagents-vault --location <region>
az backup protection enable-for-azurefileshare \
  --vault-name kagents-vault -g <rg> \
  --storage-account <storage-account> \
  --azure-file-share <share-name> \
  --policy-name DefaultPolicy
```

The default policy is daily with 30-day retention. Override per the [Azure docs](https://learn.microsoft.com/en-us/azure/backup/manage-afs-backup).

## Performance tuning

The dominant workload is small synchronous writes (mailbox JSON updates) and small synchronous reads (mailbox polls). Raw throughput matters less than IOPS and metadata-op latency.

### EFS

- **Throughput mode**: `elastic` is the right default. Pay per byte, scale automatically. Switch to `provisioned` only if you measure consistent saturation in CloudWatch's `BurstCreditBalance` metric.
- **Performance mode**: `generalPurpose` for <7,000 file ops/sec total across all teams (the typical case). `maxIO` only if you exceed that; it adds 1-3ms latency per op which hurts mailbox round-trips.
- **Mount options**: defaults are fine. The CSI driver applies `nfsvers=4.1, rsize=1048576, wsize=1048576` by default.

### Filestore

- **Tier**: `standard` is HDD-backed and fine for mailbox-polling workloads. Move to `premium` only if you measure IOPS-bound saturation under load (rare with kagents).
- **Capacity scaling**: Filestore IOPS scale linearly with provisioned capacity. If a single shared instance is saturated by many teams, double the capacity rather than splitting into multiple instances.

### Azure Files (Premium NFS)

- **Mount option `nconnect=4`** is the single biggest performance win. Without it, expect 2-3x slower mailbox round-trips. Set it in the StorageClass. See the [AKS install guide](../install/aks.md#3-create-the-storageclass).
- **Provisioned IOPS**: Azure Files Premium gives baseline IOPS proportional to provisioned size (1 IOPS per GiB). For a 100 GiB share, you get ~100 IOPS baseline + bursting. Raise capacity for more IOPS, not for more storage you don't need.

## Monitoring storage health

Use the Prometheus metrics the chart exposes (see the [Operations explanation](../../explanation/operations.md)) plus your cloud's native metrics:

- **EFS**: `IOBytes`, `BurstCreditBalance`, `ClientConnections` in CloudWatch
- **Filestore**: `nfs/server/operation_count`, `nfs/server/free_bytes_percent` in Cloud Monitoring
- **Azure Files**: `Transactions`, `SuccessE2ELatency` in Azure Monitor

A sudden spike in operation count without a corresponding rise in active teams usually indicates a stuck-poll loop in one team. `kubectl describe agentteam <name>` to investigate.

## Where to look next

- [Coordination protocol](../../explanation/coordination.md). What the storage is actually carrying
- [Set budget alerts](budget-alerts.md). Wiring cost overruns into your alert pipeline
- [Expose the dashboard](expose-dashboard.md). Visual storage-load view
