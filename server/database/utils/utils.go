// Copyright AGNTCY Contributors (https://github.com/agntcy)
// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"fmt"
	"strconv"
	"strings"

	searchv1 "github.com/agntcy/dir/api/search/v1"
	"github.com/agntcy/dir/server/types"
	"github.com/agntcy/dir/utils/logging"
)

var logger = logging.Logger("database/utils")

// choose returns with when negate is false and without when negate is true.
// Lets each QueryToFilters case pick include-vs-exclude in one line without
// adding a branch to a switch already carrying gocognit/cyclop/gocyclo nolints.
func choose[T any](negate bool, with, without func(...T) types.FilterOption) func(...T) types.FilterOption {
	if negate {
		return without
	}

	return with
}

// valueFilter pairs the include and exclude constructors for one query type.
type valueFilter[T any] struct {
	with    func(...T) types.FilterOption
	without func(...T) types.FilterOption
}

// stringFilter additionally says how the raw query value is conditioned.
type stringFilter struct {
	valueFilter[string]

	// normalize is applied before the value reaches the filter; nil passes it
	// through.
	normalize func(string) string

	// skipEmpty drops values that are blank once trimmed. Not universal: the
	// older query types accept a blank value and keep doing so, since a filter
	// that silently disappears would be a behaviour change.
	skipEmpty bool
}

// stringFilters, uintFilters and boolFilters hold the query types whose
// handling is mechanical, keeping the switch in QueryToFilters down to the
// cases that actually parse something.
var stringFilters = map[searchv1.RecordQueryType]stringFilter{
	searchv1.RecordQueryType_RECORD_QUERY_TYPE_NAME: {
		valueFilter: valueFilter[string]{types.WithNames, types.WithoutNames},
	},
	searchv1.RecordQueryType_RECORD_QUERY_TYPE_VERSION: {
		valueFilter: valueFilter[string]{types.WithVersions, types.WithoutVersions},
	},
	searchv1.RecordQueryType_RECORD_QUERY_TYPE_SKILL_NAME: {
		valueFilter: valueFilter[string]{types.WithSkillNames, types.WithoutSkillNames},
	},
	searchv1.RecordQueryType_RECORD_QUERY_TYPE_DOMAIN_NAME: {
		valueFilter: valueFilter[string]{types.WithDomainNames, types.WithoutDomainNames},
	},
	searchv1.RecordQueryType_RECORD_QUERY_TYPE_CREATED_AT: {
		valueFilter: valueFilter[string]{types.WithCreatedAts, types.WithoutCreatedAts},
	},
	searchv1.RecordQueryType_RECORD_QUERY_TYPE_AUTHOR: {
		valueFilter: valueFilter[string]{types.WithAuthors, types.WithoutAuthors},
	},
	searchv1.RecordQueryType_RECORD_QUERY_TYPE_SCHEMA_VERSION: {
		valueFilter: valueFilter[string]{types.WithSchemaVersions, types.WithoutSchemaVersions},
	},
	searchv1.RecordQueryType_RECORD_QUERY_TYPE_MODULE_NAME: {
		valueFilter: valueFilter[string]{types.WithModuleNames, types.WithoutModuleNames},
		skipEmpty:   true,
	},
	searchv1.RecordQueryType_RECORD_QUERY_TYPE_DESCRIPTION: {
		valueFilter: valueFilter[string]{types.WithDescriptions, types.WithoutDescriptions},
		skipEmpty:   true,
	},
	searchv1.RecordQueryType_RECORD_QUERY_TYPE_SCAN_SEVERITY: {
		valueFilter: valueFilter[string]{types.WithScanSeverities, types.WithoutScanSeverities},
		// Stored as the proto enum name suffix, which is upper case.
		normalize: strings.ToUpper,
		skipEmpty: true,
	},
	searchv1.RecordQueryType_RECORD_QUERY_TYPE_SCAN_STATUS: {
		valueFilter: valueFilter[string]{types.WithScanStatuses, types.WithoutScanStatuses},
		// Stored lowercase-hyphen. Matching is case-insensitive anyway, so
		// this only normalises what reaches a log line.
		normalize: strings.ToLower,
		skipEmpty: true,
	},
	searchv1.RecordQueryType_RECORD_QUERY_TYPE_SCAN_FAILURE_REASON: {
		valueFilter: valueFilter[string]{types.WithScanFailureReasons, types.WithoutScanFailureReasons},
		normalize:   strings.ToLower,
		skipEmpty:   true,
	},
}

var uintFilters = map[searchv1.RecordQueryType]struct {
	valueFilter[uint64]

	// label names the field in the parse error; the type's own String() is the
	// unwieldy RECORD_QUERY_TYPE_ form.
	label string
}{
	searchv1.RecordQueryType_RECORD_QUERY_TYPE_SKILL_ID: {
		valueFilter[uint64]{types.WithSkillIDs, types.WithoutSkillIDs}, "skill ID",
	},
	searchv1.RecordQueryType_RECORD_QUERY_TYPE_DOMAIN_ID: {
		valueFilter[uint64]{types.WithDomainIDs, types.WithoutDomainIDs}, "domain ID",
	},
	searchv1.RecordQueryType_RECORD_QUERY_TYPE_MODULE_ID: {
		valueFilter[uint64]{types.WithModuleIDs, types.WithoutModuleIDs}, "module ID",
	},
}

// boolFilters have no separate exclude constructor: negation flips the value.
var boolFilters = map[searchv1.RecordQueryType]func(bool) types.FilterOption{
	searchv1.RecordQueryType_RECORD_QUERY_TYPE_VERIFIED:  types.WithVerified,
	searchv1.RecordQueryType_RECORD_QUERY_TYPE_TRUSTED:   types.WithTrusted,
	searchv1.RecordQueryType_RECORD_QUERY_TYPE_SCAN_SAFE: types.WithScanSafe,
}

// tableFilter resolves a query against the lookup tables above. Returns
// ok=false for a type that is not table-driven, and ok=true with a nil option
// for a recognised type whose value was dropped.
func tableFilter(query *searchv1.RecordQuery) (types.FilterOption, bool, error) {
	if f, found := stringFilters[query.GetType()]; found {
		value := query.GetValue()
		if f.skipEmpty && strings.TrimSpace(value) == "" {
			return nil, true, nil
		}

		if f.normalize != nil {
			value = f.normalize(value)
		}

		return choose(query.GetNegate(), f.with, f.without)(value), true, nil
	}

	if f, found := uintFilters[query.GetType()]; found {
		parsed, err := strconv.ParseUint(query.GetValue(), 10, 64)
		if err != nil {
			return nil, true, fmt.Errorf("failed to parse %s %q: %w", f.label, query.GetValue(), err)
		}

		return choose(query.GetNegate(), f.with, f.without)(parsed), true, nil
	}

	if f, found := boolFilters[query.GetType()]; found {
		return f(strings.EqualFold(query.GetValue(), "true") != query.GetNegate()), true, nil
	}

	return nil, false, nil
}

// ParseComparisonOperator parses a value that may have an operator prefix (>=, >, <=, <, =).
// Returns the operator and the actual value. If no operator prefix, returns empty operator.
func ParseComparisonOperator(value string) (string, string) {
	// Check for two-character operators first
	if op, found := strings.CutPrefix(value, ">="); found {
		return ">=", op
	}

	if op, found := strings.CutPrefix(value, "<="); found {
		return "<=", op
	}

	if op, found := strings.CutPrefix(value, ">"); found {
		return ">", op
	}

	if op, found := strings.CutPrefix(value, "<"); found {
		return "<", op
	}

	if op, found := strings.CutPrefix(value, "="); found {
		return "=", op
	}

	// No operator prefix
	return "", value
}

// BuildComparisonConditions builds SQL conditions for values with comparison operators.
// Only values with operator prefixes (>=, >, <=, <, =) are processed as comparisons (AND logic).
// Values without operators are processed as wildcards (OR logic).
// If both are present, they are combined with OR.
func BuildComparisonConditions(column string, values []string) (string, []any) {
	if len(values) == 0 {
		return "", nil
	}

	var comparisonConditions []string

	var comparisonArgs []any

	var wildcardValues []string

	// Separate comparison operators from regular values.
	for _, value := range values {
		op, actualValue := ParseComparisonOperator(value)
		if op != "" {
			comparisonConditions = append(comparisonConditions, fmt.Sprintf("%s %s ?", column, op))
			comparisonArgs = append(comparisonArgs, actualValue)
		} else {
			wildcardValues = append(wildcardValues, value)
		}
	}

	var allConditions []string

	var allArgs []any

	// Comparison conditions are AND'd together (e.g., >= 1.0 AND < 2.0).
	if len(comparisonConditions) > 0 {
		allConditions = append(allConditions, "("+strings.Join(comparisonConditions, " AND ")+")")
		allArgs = append(allArgs, comparisonArgs...)
	}

	// Wildcard conditions are OR'd together
	if len(wildcardValues) > 0 {
		wildcardCondition, wildcardArgs := BuildWildcardCondition(column, wildcardValues)
		if wildcardCondition != "" {
			allConditions = append(allConditions, "("+wildcardCondition+")")
			allArgs = append(allArgs, wildcardArgs...)
		}
	}

	if len(allConditions) == 0 {
		return "", nil
	}

	// If we have both comparison and wildcard, OR them together
	return strings.Join(allConditions, " OR "), allArgs
}

// structuredFilters are the query types whose value has to be taken apart
// rather than passed through.
var structuredFilters = map[searchv1.RecordQueryType]func(*searchv1.RecordQuery) []types.FilterOption{
	searchv1.RecordQueryType_RECORD_QUERY_TYPE_LOCATOR:    locatorFilters,
	searchv1.RecordQueryType_RECORD_QUERY_TYPE_ANNOTATION: annotationFilters,
}

// locatorFilters splits a locator query into its type and URL parts.
//
// Nominally "type:url", but a wildcard may stand in for either half and a bare
// value may be either one, so the shape has to be inferred.
func locatorFilters(query *searchv1.RecordQuery) []types.FilterOption {
	l := strings.SplitN(query.GetValue(), ":", 2) //nolint:mnd

	locatorTypesFn := choose(query.GetNegate(), types.WithLocatorTypes, types.WithoutLocatorTypes)
	locatorURLsFn := choose(query.GetNegate(), types.WithLocatorURLs, types.WithoutLocatorURLs)

	if len(l) == 1 {
		// A leading wildcard means the whole value is a URL pattern.
		// Example: "*marketing-strategy"
		if strings.HasPrefix(l[0], "*") {
			return []types.FilterOption{locatorURLsFn(l[0])}
		}

		if strings.TrimSpace(l[0]) != "" {
			return []types.FilterOption{locatorTypesFn(l[0])}
		}

		return nil
	}

	// A "//" after the colon makes it a scheme separator, so a wildcard before
	// it is a wildcard scheme and the whole value is one URL pattern.
	// Example: "*://ghcr.io/agntcy/marketing-strategy"
	if strings.HasPrefix(l[1], "//") && strings.HasPrefix(l[0], "*") {
		return []types.FilterOption{locatorURLsFn(query.GetValue())}
	}

	var options []types.FilterOption

	if strings.TrimSpace(l[0]) != "" {
		options = append(options, locatorTypesFn(l[0]))
	}

	if strings.TrimSpace(l[1]) != "" {
		options = append(options, locatorURLsFn(l[1]))
	}

	return options
}

// annotationFilters splits a "key:value" annotation query. A bare key matches
// any value.
func annotationFilters(query *searchv1.RecordQuery) []types.FilterOption {
	parts := strings.SplitN(query.GetValue(), ":", 2) //nolint:mnd

	keysFn := choose(query.GetNegate(), types.WithAnnotationKeys, types.WithoutAnnotationKeys)
	valuesFn := choose(query.GetNegate(), types.WithAnnotationValues, types.WithoutAnnotationValues)

	if len(parts) == 1 {
		return []types.FilterOption{keysFn(parts[0])}
	}

	if parts[0] == "" {
		logger.Warn("Annotation query has empty key, skipping", "value", query.GetValue())

		return nil
	}

	options := []types.FilterOption{keysFn(parts[0])}

	if parts[1] != "" {
		options = append(options, valuesFn(parts[1]))
	}

	return options
}

// QueryToFilters translates search queries into database filter options.
func QueryToFilters(queries []*searchv1.RecordQuery) ([]types.FilterOption, error) {
	var options []types.FilterOption

	for _, query := range queries {
		option, handled, err := tableFilter(query)
		if err != nil {
			return nil, err
		}

		if handled {
			if option != nil {
				options = append(options, option)
			}

			continue
		}

		if parse, found := structuredFilters[query.GetType()]; found {
			options = append(options, parse(query)...)

			continue
		}

		if query.GetType() == searchv1.RecordQueryType_RECORD_QUERY_TYPE_UNSPECIFIED {
			logger.Warn("Unspecified query type, skipping", "query", query)

			continue
		}

		// Reached when a newer client sends a type this server predates.
		logger.Warn("Unknown query type", "type", query.GetType())
	}

	return options, nil
}
