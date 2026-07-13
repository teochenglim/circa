{{- define "circa.fullname" -}}
{{ .Release.Name }}
{{- end -}}

{{- define "circa.labels" -}}
app: {{ include "circa.fullname" . }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}
