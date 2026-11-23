{{- define "obs-notification-router.fullname" -}}
{{ .Release.Name }}-obs-notification-router
{{- end -}}

{{- define "obs-notification-router.labels" -}}
app.kubernetes.io/name: obs-notification-router
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "obs-notification-router.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "obs-notification-router.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}
