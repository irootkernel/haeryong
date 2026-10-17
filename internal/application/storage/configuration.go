package storage

import "haeryong.local/haeryong/internal/domain"

type ConfigurationTx interface {
	StagingSummary() (domain.StagingSummary, error)
	Configuration() (domain.Configuration, error)
	ConfigurationPlan(string) (domain.ConfigurationPlan, error)
	PutConfigurationPlan(domain.ConfigurationPlan) error
	ApplyProjectConfiguration(string, string, string, bool, int64) error
}
