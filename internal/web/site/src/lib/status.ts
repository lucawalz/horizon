export const severities = ['success', 'info', 'attention', 'neutral', 'danger'] as const

export type Severity = (typeof severities)[number]

const severityByStatus: Record<string, Severity> = {
  active: 'success',
  pending: 'info',
  provisioning: 'info',
  expiring: 'attention',
  degraded: 'attention',
  released: 'neutral',
  rejected: 'danger',
}

export function severityForStatus(status: string): Severity {
  return severityByStatus[status.toLowerCase()] ?? 'neutral'
}
