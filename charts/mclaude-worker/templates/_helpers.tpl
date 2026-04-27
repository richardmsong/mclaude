{{/*
Expand the name of the chart.
*/}}
{{- define "mclaude-worker.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "mclaude-worker.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Chart label value.
*/}}
{{- define "mclaude-worker.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Namespace to use.
*/}}
{{- define "mclaude-worker.namespace" -}}
{{- .Values.namespace.name }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "mclaude-worker.labels" -}}
helm.sh/chart: {{ include "mclaude-worker.chart" . }}
{{ include "mclaude-worker.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "mclaude-worker.selectorLabels" -}}
app.kubernetes.io/name: {{ include "mclaude-worker.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
ServiceAccount name for the controller.
*/}}
{{- define "mclaude-worker.controllerServiceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (printf "%s-controller" (include "mclaude-worker.fullname" .)) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Resolve image reference. Respects global.imageRegistry override.
Usage: {{ include "mclaude-worker.image" (dict "imageValues" .Values.nats.image "global" .Values.global) }}
*/}}
{{- define "mclaude-worker.image" -}}
{{- $registry := .imageValues.registry -}}
{{- $repository := .imageValues.repository -}}
{{- $tag := .imageValues.tag -}}
{{- if and .global .global.imageRegistry .global.imageRegistry }}
{{- $registry = .global.imageRegistry }}
{{- end }}
{{- if $registry }}
{{- printf "%s/%s:%s" $registry $repository $tag }}
{{- else }}
{{- printf "%s:%s" $repository $tag }}
{{- end }}
{{- end }}

{{/*
Image pull secrets merged from global and local.
*/}}
{{- define "mclaude-worker.imagePullSecrets" -}}
{{- $global := .Values.global.imagePullSecrets | default list -}}
{{- if $global }}
imagePullSecrets:
{{- range $global }}
  - name: {{ . }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Standard security context for non-root containers.
*/}}
{{- define "mclaude-worker.securityContext" -}}
securityContext:
  runAsNonRoot: true
  runAsUser: 1000
  runAsGroup: 1000
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: false
  capabilities:
    drop:
      - ALL
{{- end }}

{{/*
Standard pod security context.
*/}}
{{- define "mclaude-worker.podSecurityContext" -}}
securityContext:
  runAsNonRoot: true
  runAsUser: 1000
  runAsGroup: 1000
  fsGroup: 1000
{{- end }}
