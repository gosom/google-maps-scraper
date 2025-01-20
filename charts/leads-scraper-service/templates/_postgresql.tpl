{{/*
Generate PostgreSQL DSN
*/}}
{{- define "leads-scraper-service.postgresql.dsn" -}}
{{- if .Values.postgresql.enabled -}}
postgresql://{{ .Values.postgresql.auth.username }}:{{ .Values.postgresql.auth.password }}@{{ include "leads-scraper-service.fullname" . }}-postgresql:{{ .Values.postgresql.primary.service.ports.postgresql }}/{{ .Values.postgresql.auth.database }}
{{- else -}}
{{- .Values.config.database.dsn -}}
{{- end -}}
{{- end -}} 