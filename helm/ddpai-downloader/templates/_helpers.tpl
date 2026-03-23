{{- define "ddpai-downloader.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "ddpai-downloader.name" -}}
{{- default "ddpai-downloader" .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end }}
