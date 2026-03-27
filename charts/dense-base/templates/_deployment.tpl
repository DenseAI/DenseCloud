{{- define "dense-base.deployment" -}}
{{- $root := .root -}}
{{- $v := .values -}}
{{- $modelEnabled := ne $v.model.source "none" -}}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "dense-base.fullname" . }}
  labels:
    {{- include "dense-base.labels" . | nindent 4 }}
  {{- with $v.deploymentAnnotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
spec:
  replicas: {{ $v.replicaCount }}
  {{- if gt (int (default 0 $v.minReadySeconds)) 0 }}
  minReadySeconds: {{ $v.minReadySeconds }}
  {{- end }}
  {{- if gt (int (default 0 $v.progressDeadlineSeconds)) 0 }}
  progressDeadlineSeconds: {{ $v.progressDeadlineSeconds }}
  {{- end }}
  {{- with $v.updateStrategy }}
  strategy:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  selector:
    matchLabels:
      {{- include "dense-base.selectorLabels" . | nindent 6 }}
  template:
    metadata:
      labels:
        {{- include "dense-base.selectorLabels" . | nindent 8 }}
        {{- with $v.podLabels }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
      {{- if or $v.podAnnotations (and $v.grpc.enabled $v.grpc.tls.enabled $v.grpc.tls.secretReloadAnnotations) }}
      annotations:
        {{- with $v.podAnnotations }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
        {{- if and $v.grpc.enabled $v.grpc.tls.enabled $v.grpc.tls.secretReloadAnnotations }}
        {{- toYaml $v.grpc.tls.secretReloadAnnotations | nindent 8 }}
        {{- end }}
      {{- end }}
    spec:
      {{- with $v.imagePullSecrets }}
      imagePullSecrets:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      serviceAccountName: {{ include "dense-base.serviceAccountName" . }}
      automountServiceAccountToken: {{ $v.serviceAccount.automount }}
      terminationGracePeriodSeconds: {{ $v.terminationGracePeriodSeconds }}
      securityContext:
        {{- toYaml $v.podSecurityContext | nindent 8 }}
      {{- if $v.priorityClassName }}
      priorityClassName: {{ $v.priorityClassName | quote }}
      {{- end }}
      {{- if $v.runtimeClassName }}
      runtimeClassName: {{ $v.runtimeClassName | quote }}
      {{- end }}
      {{- if or $v.initContainers $v.extraInitContainers }}
      initContainers:
        {{- with $v.initContainers }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
        {{- with $v.extraInitContainers }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
      {{- end }}
      containers:
        - name: {{ include "dense-base.name" . }}
          image: "{{ $v.image.repository }}:{{ default $root.Chart.AppVersion $v.image.tag }}"
          imagePullPolicy: {{ $v.image.pullPolicy }}
          securityContext:
            {{- toYaml $v.securityContext | nindent 12 }}
          ports:
            - name: http
              containerPort: 8080
              protocol: TCP
            {{- if $v.grpc.enabled }}
            - name: grpc
              containerPort: {{ $v.grpc.port }}
              protocol: TCP
            {{- end }}
          {{- if or $modelEnabled $v.env $v.extraEnv }}
          env:
            {{- if $modelEnabled }}
            - name: {{ default "MODEL_PATH" $v.model.envVarName }}
              value: "{{ $v.model.path }}/{{ $v.model.filename }}"
            {{- end }}
            {{- with $v.env }}
            {{- toYaml . | nindent 12 }}
            {{- end }}
            {{- with $v.extraEnv }}
            {{- toYaml . | nindent 12 }}
            {{- end }}
          {{- end }}
          {{- with $v.extraEnvFrom }}
          envFrom:
            {{- toYaml . | nindent 12 }}
          {{- end }}
          {{- if $v.probes.enabled }}
          livenessProbe:
            httpGet:
              path: {{ $v.probes.liveness.path }}
              port: http
            initialDelaySeconds: {{ $v.probes.liveness.initialDelaySeconds }}
            periodSeconds: {{ $v.probes.liveness.periodSeconds }}
            timeoutSeconds: {{ $v.probes.liveness.timeoutSeconds }}
            failureThreshold: {{ $v.probes.liveness.failureThreshold }}
          readinessProbe:
            httpGet:
              path: {{ $v.probes.readiness.path }}
              port: http
            initialDelaySeconds: {{ $v.probes.readiness.initialDelaySeconds }}
            periodSeconds: {{ $v.probes.readiness.periodSeconds }}
            timeoutSeconds: {{ $v.probes.readiness.timeoutSeconds }}
            failureThreshold: {{ $v.probes.readiness.failureThreshold }}
          startupProbe:
            httpGet:
              path: {{ $v.probes.startup.path }}
              port: http
            initialDelaySeconds: {{ $v.probes.startup.initialDelaySeconds }}
            periodSeconds: {{ $v.probes.startup.periodSeconds }}
            timeoutSeconds: {{ $v.probes.startup.timeoutSeconds }}
            failureThreshold: {{ $v.probes.startup.failureThreshold }}
          {{- end }}
          {{- with $v.resources }}
          resources:
            {{- toYaml . | nindent 12 }}
          {{- end }}
          {{- if gt (int (default 0 $v.lifecycle.preStopSleepSeconds)) 0 }}
          lifecycle:
            preStop:
              exec:
                command: ["/bin/sh", "-c", "sleep {{ int $v.lifecycle.preStopSleepSeconds }}"]
          {{- end }}
          volumeMounts:
            {{- if $modelEnabled }}
            - name: model-volume
              mountPath: {{ $v.model.path }}
            {{- end }}
            {{- if $v.tmpVolume.enabled }}
            - name: tmp-volume
              mountPath: {{ default "/tmp" $v.tmpVolume.mountPath }}
            {{- end }}
            {{- if and $v.grpc.enabled $v.grpc.tls.enabled }}
            - name: grpc-tls-cert
              mountPath: {{ dir $v.grpc.tls.certFile }}
              readOnly: true
            {{- if and $v.grpc.tls.requireClientCert $v.grpc.tls.clientCASecret }}
            - name: grpc-client-ca
              mountPath: {{ dir $v.grpc.tls.clientCAFile }}
              readOnly: true
            {{- end }}
            {{- end }}
            {{- with $v.extraVolumeMounts }}
            {{- toYaml . | nindent 12 }}
            {{- end }}
      {{- with $v.extraContainers }}
        {{- toYaml . | nindent 8 }}
      {{- end }}
      volumes:
        {{- if $modelEnabled }}
        - name: model-volume
          {{- if eq $v.model.source "pvc" }}
          persistentVolumeClaim:
            claimName: {{ $v.model.existingClaim | default (printf "%s-models" (include "dense-base.fullname" .)) }}
          {{- else if eq $v.model.source "hostPath" }}
          hostPath:
            path: {{ $v.model.hostPath | quote }}
            type: {{ $v.model.hostPathType | quote }}
          {{- else if eq $v.model.source "emptyDir" }}
          emptyDir:
            sizeLimit: {{ $v.model.emptyDir.sizeLimit | quote }}
            {{- if $v.model.emptyDir.medium }}
            medium: {{ $v.model.emptyDir.medium | quote }}
            {{- end }}
          {{- end }}
        {{- end }}
        {{- if $v.tmpVolume.enabled }}
        - name: tmp-volume
          emptyDir:
            {{- if $v.tmpVolume.sizeLimit }}
            sizeLimit: {{ $v.tmpVolume.sizeLimit | quote }}
            {{- end }}
            {{- if $v.tmpVolume.medium }}
            medium: {{ $v.tmpVolume.medium | quote }}
            {{- end }}
        {{- end }}
        {{- if and $v.grpc.enabled $v.grpc.tls.enabled }}
        - name: grpc-tls-cert
          secret:
            secretName: {{ $v.grpc.tls.certSecret | quote }}
            optional: false
        {{- if and $v.grpc.tls.requireClientCert $v.grpc.tls.clientCASecret }}
        - name: grpc-client-ca
          secret:
            secretName: {{ $v.grpc.tls.clientCASecret | quote }}
            optional: false
        {{- end }}
        {{- end }}
        {{- with $v.extraVolumes }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
      {{- with $v.nodeSelector }}
      nodeSelector:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with $v.affinity }}
      affinity:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with $v.tolerations }}
      tolerations:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with $v.topologySpreadConstraints }}
      topologySpreadConstraints:
        {{- toYaml . | nindent 8 }}
      {{- end }}
{{- end -}}
