export type ConditionStatus = 'True' | 'False' | 'Unknown'

type KnownLeasePhase =
  | 'Pending'
  | 'Provisioning'
  | 'Active'
  | 'Expiring'
  | 'Released'
  | 'Degraded'

type KnownInstancePhase = 'Intended' | 'Created' | 'Joined' | 'Draining' | 'Released'

type KnownInstanceStage = 'AwaitingInstance' | 'AwaitingRegistration' | 'AwaitingReady' | 'Ready'

type KnownCatalogueState =
  | 'NoSelection'
  | 'CatalogueAbsent'
  | 'CatalogueUnfilled'
  | 'NoMatch'
  | 'ReadFailed'
  | 'Listed'

// a cluster may run a newer crd than the binary answering these requests, so an unlisted value stays representable
type Open<T extends string> = T | (string & {})

export type LeasePhase = Open<KnownLeasePhase>
export type InstancePhase = Open<KnownInstancePhase>
export type InstanceStage = Open<KnownInstanceStage>
export type CatalogueState = Open<KnownCatalogueState>
export type Architecture = Open<'x86' | 'arm'>
export type CPUType = Open<'shared' | 'dedicated'>
export type SizingStrategy = Open<'LowestPrice' | 'LowestPricePerCore'>

export interface LeaseSummary {
  name: string
  replicas: number
  region: string
  phase: LeasePhase | null
  expiresAt: string | null
  ready: ConditionStatus | null
  armed: ConditionStatus | null
  createdAt: string
  instanceType: string | null
  readyAt: string | null
  releasedAt: string | null
}

export interface LeaseListResponse {
  leases: LeaseSummary[]
  observedAt: string
}

export interface ConditionEntry {
  type: string
  status: ConditionStatus
  reason: string | null
  message: string | null
  lastTransitionTime: string
}

export interface LeaseInstance {
  name: string
  providerID: string | null
  nodeName: string | null
  phase: InstancePhase
  stage: InstanceStage | null
  createdAt: string | null
  lastError: string | null
}

export interface LeaseRequirements {
  minCPU: number
  minMemory: string | null
  architecture: Architecture
  cpuType: CPUType | null
  strategy: SizingStrategy | null
}

export interface RejectedCandidates {
  reason: string
  count: number
}

export interface LeaseSelection {
  strategy: SizingStrategy
  chosen: string
  hourlyRate: string | null
  currency: string | null
  runnerUp: string | null
  offered: number
  qualified: number
  rejected: RejectedCandidates[]
  decidedAt: string
}

export interface LeaseDetailResponse {
  summary: LeaseSummary
  providerRef: string
  size: string | null
  requirements: LeaseRequirements | null
  selection: LeaseSelection | null
  durationSeconds: number
  teardownGraceSeconds: number | null
  workloadNamespace: string | null
  migratedWorkloads: string[]
  acceptedAt: string | null
  watchdogDeadline: string | null
  observedGeneration: number
  conditions: ConditionEntry[]
  instances: LeaseInstance[]
  observedAt: string
}

export interface ProviderConfigSummary {
  name: string
  type: string
  ready: ConditionStatus | null
  createdAt: string
}

export interface Money {
  amount: number
  currency: string
}

export interface MachineType {
  name: string
  architecture: string | null
  cpuType: string | null
  cpuCores: number
  memoryBytes: number
  diskBytes: number
  hourlyRate: Money | null
  available: boolean
  deprecated: boolean
}

export interface MachineCatalogueResponse {
  configs: ProviderConfigSummary[]
  config: string
  region: string
  state: CatalogueState
  detail: string | null
  refreshedAt: string | null
  types: MachineType[]
  observedAt: string
}

export interface LeaseRequirementsRequest {
  minCPU: number
  minMemory: string
  architecture: string
  cpuType: string
  strategy: string
}

export interface LeaseCreateRequest {
  name: string
  providerRef: string
  region: string
  size: string
  requirements: LeaseRequirementsRequest | null
  replicas: number
  durationSeconds: number
  teardownGraceSeconds: number | null
  workloadNamespace: string
}

export interface LeaseReleaseResponse {
  name: string
  detail: string
}

interface ApiErrorBody {
  status: number
  title: string
  detail: string
}

export class RequestFailed extends Error {
  status: number

  constructor(status: number, detail: string) {
    super(detail)
    this.name = 'RequestFailed'
    this.status = status
  }
}

export const notFound = 404

export const interfaceHeader = 'X-Horizon-Interface'

const interfaceHeaderValue = 'true'
const jsonContentType = 'application/json'
const createMethod = 'POST'
const releaseMethod = 'DELETE'
const configQueryKey = 'config'
const regionQueryKey = 'region'

export const leasesPath = '/api/leases'
const machinesBasePath = '/api/machines'

export function leasePath(name: string): string {
  return `${leasesPath}/${encodeURIComponent(name)}`
}

export function machinesPath(config: string, region: string): string {
  const query = new URLSearchParams({ [configQueryKey]: config, [regionQueryKey]: region })
  return `${machinesBasePath}?${query.toString()}`
}

async function failureDetail(response: Response): Promise<string> {
  try {
    const body = (await response.json()) as Partial<ApiErrorBody>
    if (typeof body.detail === 'string' && body.detail !== '') return body.detail
    if (typeof body.title === 'string' && body.title !== '') return body.title
  } catch {
    // a body that is not the error shape leaves only the status line to report
  }
  return response.statusText || `the request failed with status ${response.status}`
}

async function submit<T>(path: string, init: RequestInit): Promise<T> {
  const response = await fetch(path, init)
  if (!response.ok) {
    throw new RequestFailed(response.status, await failureDetail(response))
  }
  return (await response.json()) as T
}

function read<T>(path: string): Promise<T> {
  return submit<T>(path, { headers: { Accept: jsonContentType } })
}

// a simple form post cannot set a header, and a scripted cross-origin request that tries must first pass a preflight this server never answers
function change<T>(path: string, method: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {
    Accept: jsonContentType,
    [interfaceHeader]: interfaceHeaderValue,
  }
  if (body !== undefined) headers['Content-Type'] = jsonContentType
  return submit<T>(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
}

export function fetchLeases(): Promise<LeaseListResponse> {
  return read<LeaseListResponse>(leasesPath)
}

export function fetchLease(name: string): Promise<LeaseDetailResponse> {
  return read<LeaseDetailResponse>(leasePath(name))
}

export function fetchMachines(config: string, region: string): Promise<MachineCatalogueResponse> {
  return read<MachineCatalogueResponse>(machinesPath(config, region))
}

export function createLease(request: LeaseCreateRequest): Promise<LeaseDetailResponse> {
  return change<LeaseDetailResponse>(leasesPath, createMethod, request)
}

export function releaseLease(name: string): Promise<LeaseReleaseResponse> {
  return change<LeaseReleaseResponse>(leasePath(name), releaseMethod)
}
