package metrics

const (
	labelCondition    = "condition"
	labelInstanceType = "instance_type"
	labelOperation    = "operation"
	labelOutcome      = "outcome"
	labelPath         = "path"
	labelPhase        = "phase"
	labelProvider     = "provider"
	labelReason       = "reason"
	labelRegion       = "region"
	labelResult       = "result"
	labelSelection    = "selection"
	labelStatus       = "status"
	labelStrategy     = "strategy"
)

type Selection string

const (
	SelectionPinned             Selection = "pinned"
	SelectionLowestPrice        Selection = "lowest-price"
	SelectionLowestPricePerCore Selection = "lowest-price-per-core"
)

type Path string

const (
	PathController Path = "controller"
	PathNode       Path = "node"
	PathOrphan     Path = "orphan"
	PathExternal   Path = "external"
)

type Outcome string

const (
	OutcomeReleased         Outcome = "released"
	OutcomeReleasedDegraded Outcome = "released_degraded"
	OutcomeRejected         Outcome = "rejected"
)

type Reason string

const (
	ReasonNoMatch              Reason = "no_match"
	ReasonCatalogueUnavailable Reason = "catalogue_unavailable"
	ReasonRegionUnavailable    Reason = "region_unavailable"
)

type Result string

const (
	ResultSuccess  Result = "success"
	ResultFailure  Result = "failure"
	ResultNotFound Result = "not_found"
	ResultCanceled Result = "canceled"
)

type Operation string

const (
	OperationCreate            Operation = "create"
	OperationGet               Operation = "get"
	OperationList              Operation = "list"
	OperationDelete            Operation = "delete"
	OperationListInstanceTypes Operation = "list_instance_types"
)
