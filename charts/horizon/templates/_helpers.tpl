{{- define "horizon.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "horizon.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "horizon.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "horizon.labels" -}}
helm.sh/chart: {{ include "horizon.chart" . }}
{{ include "horizon.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "horizon.selectorLabels" -}}
app.kubernetes.io/name: {{ include "horizon.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "horizon.controllerComponent" -}}controller{{- end -}}

{{- define "horizon.controllerSelectorLabels" -}}
{{ include "horizon.selectorLabels" . }}
app.kubernetes.io/component: {{ include "horizon.controllerComponent" . }}
{{- end -}}

{{- define "horizon.controllerLabels" -}}
{{ include "horizon.labels" . }}
app.kubernetes.io/component: {{ include "horizon.controllerComponent" . }}
{{- end -}}

{{- define "horizon.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "horizon.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "horizon.interfaceComponent" -}}interface{{- end -}}

{{- define "horizon.interfaceFullname" -}}
{{- printf "%s-%s" (include "horizon.fullname" .) (include "horizon.interfaceComponent" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "horizon.interfaceOperatorRoleName" -}}
{{- printf "%s-operator" (include "horizon.interfaceFullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "horizon.interfaceSelectorLabels" -}}
{{ include "horizon.selectorLabels" . }}
app.kubernetes.io/component: {{ include "horizon.interfaceComponent" . }}
{{- end -}}

{{- define "horizon.interfaceLabels" -}}
{{ include "horizon.labels" . }}
app.kubernetes.io/component: {{ include "horizon.interfaceComponent" . }}
{{- end -}}

{{- define "horizon.interfaceServiceAccountName" -}}
{{- if .Values.ui.serviceAccount.create -}}
{{- default (include "horizon.interfaceFullname" .) .Values.ui.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.ui.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "horizon.interfaceIdentity" -}}
{{- $controller := include "horizon.serviceAccountName" . -}}
{{- $interface := include "horizon.interfaceServiceAccountName" . -}}
{{- if eq $controller $interface -}}
{{- fail (printf "ui.serviceAccount.name must not be the controller service account %s; a shared account would grant the controller impersonation" $controller) -}}
{{- end -}}
{{- $interface -}}
{{- end -}}

{{- define "horizon.interfaceArgs" -}}
{{- $missing := list -}}
{{- if not .Values.ui.oidc.issuer }}{{- $missing = append $missing "ui.oidc.issuer" -}}{{- end -}}
{{- if not .Values.ui.oidc.audience }}{{- $missing = append $missing "ui.oidc.audience" -}}{{- end -}}
{{- if $missing -}}
{{- fail (printf "ui.enabled requires OIDC verification; empty values: %s. The interface would otherwise serve cluster impersonation to unauthenticated callers" (join ", " $missing)) -}}
{{- end -}}
- serve
- {{ printf "--bind-address=%s:%v" .Values.ui.bindHost .Values.ui.port | quote }}
- {{ printf "--auth-header=%s" .Values.ui.authHeader | quote }}
- {{ printf "--oidc-issuer=%s" .Values.ui.oidc.issuer | quote }}
- {{ printf "--oidc-audience=%s" .Values.ui.oidc.audience | quote }}
- {{ printf "--username-claim=%s" .Values.ui.usernameClaim | quote }}
- {{ printf "--groups-claim=%s" .Values.ui.groupsClaim | quote }}
{{- with .Values.ui.externalOrigin }}
- {{ printf "--external-origin=%s" . | quote }}
{{- end }}
{{- end -}}

{{- define "horizon.image" -}}
{{- if .Values.image.digest -}}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest -}}
{{- else -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- if eq $tag "latest" -}}
{{- fail "image.tag must be an immutable tag; latest is rejected by cluster admission policy" -}}
{{- end -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}
{{- end -}}
