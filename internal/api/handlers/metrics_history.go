package handlers

// MetricsHistory is the Metrics tab's kind: the cluster's external metric
// servers.
func MetricsHistory() HistoryKind {
	return HistoryKind{
		Name:          "metrics",
		Categories:    objectCategories,
		Default:       "other",
		Rules:         append(append([]HistoryRule{}, objectRules...), ruleObserved),
		Select:        SelectTypes("metric_server"),
		ObjectTypes:   []string{"metric_server"},
		ObservedTypes: observedOf("metric_server"),
	}
}

// MetricServerHistory is one metric server's kind.
func MetricServerHistory() HistoryKind {
	return objectHistory("metric-server", "server_id", []string{"metric_server"}, "metric_server")
}
