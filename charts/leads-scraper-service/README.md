# leads-scraper-service

A Helm chart for deploying the Vector Leads Scraper service, which extracts and processes business leads data

![Version: 0.1.0](https://img.shields.io/badge/Version-0.1.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 1.16.0](https://img.shields.io/badge/AppVersion-1.16.0-informational?style=flat-square)

## Prerequisites

- Kubernetes 1.16+
- Helm 3.0+
- PostgreSQL database (can be deployed separately or using a dependency)
- Access to the container registry containing the leads scraper image

## Installing the Chart

Add the repository (if hosted in a Helm repository):
```bash
helm repo add vector-charts <repository-url>
helm repo update
```

To install the chart with the release name `leads-scraper`:

```bash
helm install leads-scraper ./charts/leads-scraper-service
```

## Requirements

| Repository | Name | Version |
|------------|------|---------|
| https://charts.bitnami.com/bitnami | postgresql | 12.5.6 |

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` |  |
| autoscaling.enabled | bool | `true` | Enable autoscaling |
| autoscaling.maxReplicas | int | `5` | Maximum number of replicas |
| autoscaling.minReplicas | int | `1` | Minimum number of replicas |
| autoscaling.targetCPUUtilizationPercentage | int | `95` | Target CPU utilization percentage |
| autoscaling.targetMemoryUtilizationPercentage | int | `95` | Target memory utilization percentage |
| config.aws.accessKey | string | `""` | AWS access key |
| config.aws.lambda.chunkSize | int | `100` | Lambda chunk size |
| config.aws.lambda.enabled | bool | `false` | Enable AWS Lambda integration |
| config.aws.lambda.functionName | string | `""` | Lambda function name |
| config.aws.lambda.invoker | bool | `false` | Enable Lambda invoker |
| config.aws.region | string | `"us-east-1"` | AWS region |
| config.aws.s3.bucket | string | `"leads-scraper-service"` | S3 bucket name |
| config.aws.secretKey | string | `""` | AWS secret key |
| config.database.dsn | string | "" | Database connection string (Required) Format: postgres://username:password@host:port/dbname |
| config.scraper.concurrency | int | `11` |  |
| config.scraper.depth | int | `5` |  |
| config.scraper.emailExtraction | bool | `false` |  |
| config.scraper.exitOnInactivity | string | `""` |  |
| config.scraper.fastMode | bool | `true` |  |
| config.scraper.language | string | `"en"` |  |
| config.scraper.proxies | string | `""` |  |
| config.scraper.searchRadius | int | `10000` |  |
| config.scraper.webServer | bool | `true` | Enable web server mode |
| config.scraper.zoomLevel | int | `15` |  |
| fullnameOverride | string | `""` | Override the full name |
| image.pullPolicy | string | `"Always"` | Image pull policy |
| image.repository | string | `"feelguuds/leads-scraper-service"` | Container image repository |
| image.tag | string | `"latest"` |  |
| imagePullSecrets | list | `[]` |  |
| ingress.annotations | object | `{}` |  |
| ingress.className | string | `""` | IngressClass that will be be used |
| ingress.enabled | bool | `false` | Enable ingress controller resource |
| ingress.hosts[0].host | string | `"chart-example.local"` |  |
| ingress.hosts[0].paths[0].path | string | `"/"` |  |
| ingress.hosts[0].paths[0].pathType | string | `"ImplementationSpecific"` |  |
| ingress.tls | list | `[]` |  |
| livenessProbe.httpGet.path | string | `"/health"` |  |
| livenessProbe.httpGet.port | string | `"http"` |  |
| livenessProbe.initialDelaySeconds | int | `5` |  |
| livenessProbe.periodSeconds | int | `10` |  |
| nameOverride | string | `""` | Override the chart name |
| nodeSelector | object | `{}` |  |
| podAnnotations | object | `{}` |  |
| podLabels | object | `{}` |  |
| podSecurityContext | object | `{}` |  |
| postgresql.auth.database | string | `"leads_scraper"` | PostgreSQL database name |
| postgresql.auth.existingSecret | string | `""` | Existing secret for PostgreSQL password |
| postgresql.auth.password | string | `"postgres"` | PostgreSQL password |
| postgresql.auth.username | string | `"postgres"` | PostgreSQL username |
| postgresql.enabled | bool | `true` | Enable PostgreSQL dependency |
| postgresql.primary.extraEnvVars[0].name | string | `"POSTGRESQL_MAX_CONNECTIONS"` |  |
| postgresql.primary.extraEnvVars[0].value | string | `"100"` |  |
| postgresql.primary.extraEnvVars[1].name | string | `"POSTGRESQL_SHARED_BUFFERS"` |  |
| postgresql.primary.extraEnvVars[1].value | string | `"128MB"` |  |
| postgresql.primary.persistence.enabled | bool | `true` | Enable PostgreSQL persistence |
| postgresql.primary.persistence.size | string | `"10Gi"` | PostgreSQL PVC size |
| postgresql.primary.resources.limits.cpu | string | `"1000m"` |  |
| postgresql.primary.resources.limits.memory | string | `"1Gi"` |  |
| postgresql.primary.resources.requests.cpu | string | `"100m"` |  |
| postgresql.primary.resources.requests.memory | string | `"256Mi"` |  |
| postgresql.primary.service.ports.postgresql | int | `5432` | PostgreSQL service port |
| readinessProbe.httpGet.path | string | `"/health"` |  |
| readinessProbe.httpGet.port | string | `"http"` |  |
| readinessProbe.initialDelaySeconds | int | `5` |  |
| readinessProbe.periodSeconds | int | `10` |  |
| replicaCount | int | `1` | Number of replicas for the leads scraper deployment |
| resources.limits.cpu | string | `"1000m"` |  |
| resources.limits.memory | string | `"512Mi"` |  |
| resources.requests.cpu | string | `"100m"` |  |
| resources.requests.memory | string | `"128Mi"` |  |
| securityContext | object | `{}` |  |
| service.port | int | `8080` |  |
| service.type | string | `"ClusterIP"` | Service type (ClusterIP, NodePort, LoadBalancer) |
| serviceAccount.annotations | object | `{}` | Annotations to add to the service account |
| serviceAccount.automount | bool | `true` | Automatically mount a ServiceAccount's API credentials |
| serviceAccount.create | bool | `true` | Specifies whether a service account should be created |
| serviceAccount.name | string | `""` | The name of the service account to use. If not set and create is true, a name is generated using the fullname template |
| tests.configCheck.enabled | bool | `true` | Enable config check test |
| tests.enabled | bool | `false` | Enable helm tests |
| tests.healthCheck.enabled | bool | `true` | Enable health check test |
| tests.healthCheck.path | string | `"/health"` | Health check endpoint path |
| tolerations | list | `[]` |  |
| volumeMounts | list | `[]` |  |
| volumes | list | `[]` |  |

## Values File Example

Create a `values.yaml` file to customize the installation:

```yaml
replicaCount: 2

image:
  repository: vector/leads-scraper
  tag: latest
  pullPolicy: IfNotPresent

resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 512Mi

postgresql:
  host: my-postgres-host
  port: 5432
  database: leads_scraper
  username: scraper_user
  # password should be provided via secret
```

## Installing with Custom Values

```bash
helm install leads-scraper ./charts/leads-scraper-service -f values.yaml
```

## Upgrading

To upgrade the release:
```bash
helm upgrade leads-scraper ./charts/leads-scraper-service -f values.yaml
```

## Uninstalling

To uninstall/delete the deployment:
```bash
helm uninstall leads-scraper
```

## Security Considerations

- Database credentials should be managed using Kubernetes Secrets
- Use appropriate RBAC policies
- Configure resource limits to prevent resource exhaustion
- Enable network policies as needed

## Troubleshooting

Common issues and their solutions:

1. **Pod fails to start**:
   - Check the pod logs: `kubectl logs -l app=leads-scraper`
   - Verify PostgreSQL connection details
   - Ensure sufficient resources are available

2. **Database connection issues**:
   - Verify PostgreSQL credentials
   - Check network connectivity
   - Ensure database is accessible from the cluster

## Support

For support, please file issues in the GitHub repository or contact the Vector team.

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2) 