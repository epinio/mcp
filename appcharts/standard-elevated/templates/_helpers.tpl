{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "epinio-application.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "epinio-application.labels" -}}
app.kubernetes.io/managed-by: epinio
app.kubernetes.io/part-of: {{ .Release.Namespace | quote }}
helm.sh/chart: {{ include "epinio-application.chart" . }}
{{ include "epinio-application.selectorLabels" . }}
{{- end }}

{{/*
Common annotations
*/}}
{{- define "epinio-application.annotations" -}}
epinio.io/created-by: {{ .Values.epinio.username | quote }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "epinio-application.selectorLabels" -}}
app.kubernetes.io/name: {{ .Values.epinio.appName | quote }}
app.kubernetes.io/component: application
{{- end }}

{{/*
Removes characters that are invalid for kubernetes resource names from the
given string
*/}}
{{- define "epinio-name-sanitize" -}}
{{ regexReplaceAll "[^-a-z0-9]*" . "" }}
{{- end }}

{{/*
Resource name sanitization and truncation.
- Always suffix the sha1sum (40 characters long)
- Always add an "r" prefix to make sure we don't have leading digits
- The rest of the characters up to 63 are the original string with invalid
character removed.
*/}}
{{- define "epinio-truncate" -}}
{{ print "r" (trunc 21 (include "epinio-name-sanitize" .)) "-" (sha1sum .) }}
{{- end }}

{{/*
Application listening port
*/}}
{{- define "epinio-app-listening-port" -}}
{{ default 8080 (default (dict "appListeningPort" "8080") .Values.userConfig).appListeningPort }}
{{- end }}
