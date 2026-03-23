# Helm Chart

Helm chart for deploying DDPai Downloader on Kubernetes (e.g. K3s).

## Quick start

```bash
helm install ddpai-downloader ./ddpai-downloader
```

## Health checks

| Endpoint | Purpose |
| --- | --- |
| `GET /ping` | Liveness — returns 200 if the server is running |
| `GET /health` | Readiness — returns 200 if storage (PVC) is accessible, 503 otherwise |

The chart configures Kubernetes probes:
- **Liveness:** `GET /ping` every 10s (restart if unreachable)
- **Readiness:** `GET /health` every 10s (exclude from traffic if storage unavailable)

Camera connectivity is not checked — the server is designed to wait for the camera.

## Default values

| Key | Default | Description |
| --- | --- | --- |
| `replicaCount` | `1` | Number of replicas |
| `fullnameOverride` | `"ddpai-downloader"` | Overrides Deployment/Service/PVC names (avoids `ddpai-downloader-ddpai-downloader`) |
| **Image** | | |
| `image.repository` | `ghcr.io/hansaya/ddpai_downloader` | Container image |
| `image.tag` | `"v1.0.0-alpha-2"` | Image tag |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy |
| **Environment** | | |
| `env.HTTP_PORT` | `"8080"` | HTTP health/status port |
| `env.STORAGE_PATH` | `"/media/dashcam"` | Path where recordings are stored inside the container |
| `env.RECORDING_HISTORY` | `"336h"` | How long to keep recordings (e.g. 96h, 336h) |
| `env.TIMEOUT` | `"180s"` | Download timeout |
| `env.CAM_URL` | `http://193.168.0.1` | Camera URL (override if needed) |
| `env.INTERVAL` | `30s` | Wait period between camera pings |
| `env.LOG_LEVEL` | `info` | Log level |
| **Security** | | |
| `securityContext.runAsUser` | `1004` | Run container as this user |
| `securityContext.runAsGroup` | `1004` | Run container as this group |
| `securityContext.fsGroup` | `1004` | FS group for volumes |
| **Service** | | |
| `service.type` | `ClusterIP` | Kubernetes service type |
| `service.port` | `8080` | Service port |
| **Persistence** | | |
| `persistence.enabled` | `true` | Enable persistent storage |
| `persistence.existingClaim` | `""` | Use existing PVC (e.g. `"dvr-pvc-homeio"`) |
| `persistence.mountPath` | `"/media"` | Mount path inside container |
| `persistence.storageClass` | `""` | StorageClass for new PVC |
| `persistence.size` | `10Gi` | Size when creating new PVC |
| `persistence.accessMode` | `ReadWriteOnce` | PVC access mode |
| **Other** | | |
| `podAnnotations` | `{}` | Annotations for pods |
| `resources` | `{}` | CPU/memory limits and requests |

## Override values

```bash
helm install ddpai-downloader ./ddpai-downloader \
  --set env.RECORDING_HISTORY=168h \
  --set persistence.existingClaim=my-dvr-pvc
```

Or with a values file:

```yaml
# my-values.yaml
env:
  STORAGE_PATH: "/media/dashcam"
  RECORDING_HISTORY: "168h"
  TIMEOUT: "200s"
persistence:
  enabled: true
  existingClaim: "dvr-pvc"
  mountPath: /media
```

```bash
helm install ddpai-downloader ./ddpai-downloader -f my-values.yaml
```

## Flux / HelmRelease

This chart is designed to work with [Flux](https://fluxcd.io/). Reference it from a GitRepository:

```yaml
spec:
  chart:
    spec:
      chart: ./helm/ddpai-downloader
      sourceRef:
        kind: GitRepository
        name: ddpai-downloader
        namespace: flux-system
  values:
    persistence:
      existingClaim: "dvr-pvc-homeio"
      mountPath: /media
```
