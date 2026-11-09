package store

import (
	"context"
	"regexp"

	"signal-desk/internal/config"
)

type PlannedNotification struct {
	Pattern                              *regexp.Regexp
	ID                                   int64
	Name, Channel, Customer, Destination string
	Template                             config.NotificationTemplate
	config.Policy
}

func (s *Store) NotificationPlan(ctx context.Context) ([]PlannedNotification, error) {
	items, err := listResources(ctx, s.db, "kind IN ('templates','policies','channels') AND enabled=1")
	if err != nil {
		return nil, err
	}
	templates := make(map[int64]config.NotificationTemplate)
	channels := make(map[int64]Resource)
	var policies []Resource
	for _, item := range items {
		switch item.Kind {
		case "templates":
			var tmpl config.NotificationTemplate
			if err := s.Decode(item, &tmpl); err != nil {
				return nil, err
			}
			templates[item.ID] = tmpl
		case "channels":
			channels[item.ID] = item
		case "policies":
			policies = append(policies, item)
		}
	}
	var plan []PlannedNotification
	for _, item := range policies {
		var policy config.Policy
		if err := s.Decode(item, &policy); err != nil {
			return nil, err
		}
		channel, ok := channels[policy.ChannelID]
		tmpl, exists := templates[policy.TemplateID]
		if !ok || !exists {
			continue
		}
		var cfg config.Channel
		if err := s.Decode(channel, &cfg); err != nil {
			return nil, err
		}
		customer := cfg.Customer
		if customer == "" {
			customer = channel.Name
		}
		pattern, err := policy.CompileModelPattern()
		if err != nil {
			return nil, err
		}
		plan = append(plan, PlannedNotification{ID: item.ID, Name: item.Name, Channel: channel.Name, Pattern: pattern,
			Customer: customer, Destination: channel.Source, Template: tmpl, Policy: policy})
	}
	return plan, nil
}
