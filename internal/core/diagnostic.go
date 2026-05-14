package core

type DiagnosticStatus int

const (
	StatusOK DiagnosticStatus = iota
	StatusSKIP
	StatusWARN
	StatusFAIL
)

func (s DiagnosticStatus) String() string {
	switch s {
	case StatusOK:
		return "OK"
	case StatusSKIP:
		return "SKIP"
	case StatusWARN:
		return "WARN"
	case StatusFAIL:
		return "FAIL"
	default:
		return "UNKNOWN"
	}
}

func (s DiagnosticStatus) Less(other DiagnosticStatus) bool {
	return s < other
}

func AggregateStatus(statuses ...DiagnosticStatus) DiagnosticStatus {
	aggregate := StatusOK
	for _, status := range statuses {
		if aggregate.Less(status) {
			aggregate = status
		}
	}
	return aggregate
}

type DiagnosticCheck struct {
	ID      string
	Title   string
	Status  DiagnosticStatus
	Summary string
	Details []string
}

type DiagnosticFinding struct {
	ID            string
	Status        DiagnosticStatus
	Title         string
	Summary       string
	Evidence      []string
	Remediation   string
	RelatedChecks []string
}

type DiagnosticSection struct {
	ID     string
	Title  string
	Checks []DiagnosticCheck
}

func AggregateChecks(checks []DiagnosticCheck) DiagnosticStatus {
	aggregate := StatusOK
	for _, check := range checks {
		aggregate = AggregateStatus(aggregate, check.Status)
	}
	return aggregate
}

func (s DiagnosticSection) Status() DiagnosticStatus {
	return AggregateChecks(s.Checks)
}

func AggregateSections(sections []DiagnosticSection) DiagnosticStatus {
	aggregate := StatusOK
	for _, section := range sections {
		aggregate = AggregateStatus(aggregate, section.Status())
	}
	return aggregate
}
