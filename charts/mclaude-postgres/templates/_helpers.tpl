{{/*
Expand the name of the chart.
*/}}
{{- define "mclaude-postgres.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "mclaude-postgres.fullname" -}}
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
{{- define "mclaude-postgres.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Namespace to use.
*/}}
{{- define "mclaude-postgres.namespace" -}}
{{- .Values.namespace.name }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "mclaude-postgres.labels" -}}
helm.sh/chart: {{ include "mclaude-postgres.chart" . }}
{{ include "mclaude-postgres.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "mclaude-postgres.selectorLabels" -}}
app.kubernetes.io/name: {{ include "mclaude-postgres.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
