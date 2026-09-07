package testserver

import "fmt"

// Synthetic, deterministic data for the acceptance test in brief §7.
// Nothing here is randomized — the same indices always produce the same
// resources, so tests never flake. No Synthea dependency, per the brief.

var patientFamilyNames = []string{
	"Smith", "Stone", "Johnson", "Jones", "Brown",
	"Davis", "Miller", "Wilson", "Moore", "Taylor",
	"Anderson", "Thomas", "Jackson", "White", "Harris",
}

var observationCodes = []string{"8480-6", "8462-4", "2093-3", "4548-4", "2085-9", "2571-8"}

func makePatients(n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		out[i] = map[string]any{
			"resourceType": "Patient",
			"id":           fmt.Sprintf("patient-%d", i),
			"birthDate":    fmt.Sprintf("19%02d-01-15", 50+i%40),
			"name": []any{
				map[string]any{
					"family": patientFamilyNames[i%len(patientFamilyNames)],
					"given":  []any{"Sample"},
				},
			},
			"meta": map[string]any{
				"lastUpdated": fmt.Sprintf("2024-01-%02dT00:00:00Z", 1+i%28),
			},
		}
	}
	return out
}

func makeObservations(n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		out[i] = map[string]any{
			"resourceType": "Observation",
			"id":           fmt.Sprintf("obs-%d", i),
			"status":       "final",
			"code": map[string]any{
				"coding": []any{
					map[string]any{"system": "http://loinc.org", "code": observationCodes[i%len(observationCodes)]},
				},
			},
			"subject": map[string]any{"reference": fmt.Sprintf("Patient/patient-%d", i%15)},
		}
	}
	return out
}

func makeConditions(n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		out[i] = map[string]any{
			"resourceType": "Condition",
			"id":           fmt.Sprintf("condition-%d", i),
			"subject":      map[string]any{"reference": fmt.Sprintf("Patient/patient-%d", i%15)},
		}
	}
	return out
}

func makeEncounters(n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		out[i] = map[string]any{
			"resourceType": "Encounter",
			"id":           fmt.Sprintf("encounter-%d", i),
			"status":       "finished",
		}
	}
	return out
}

func makeProcedures(n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		out[i] = map[string]any{
			"resourceType": "Procedure",
			"id":           fmt.Sprintf("procedure-%d", i),
			"status":       "completed",
		}
	}
	return out
}

func makeMedicationRequests(n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		out[i] = map[string]any{
			"resourceType": "MedicationRequest",
			"id":           fmt.Sprintf("medreq-%d", i),
			"status":       "active",
			"intent":       "order",
		}
	}
	return out
}

func makeImmunizations(n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		out[i] = map[string]any{
			"resourceType":       "Immunization",
			"id":                 fmt.Sprintf("imm-%d", i),
			"status":             "completed",
			"occurrenceDateTime": fmt.Sprintf("2024-02-%02d", 1+i%28),
		}
	}
	return out
}

func makeDiagnosticReports(n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		out[i] = map[string]any{
			"resourceType": "DiagnosticReport",
			"id":           fmt.Sprintf("diagreport-%d", i),
			"status":       "final",
			"basedOn": []any{
				map[string]any{"reference": fmt.Sprintf("ServiceRequest/sr-%d", i%4)},
			},
		}
	}
	return out
}

func makeAllergyIntolerances(n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		out[i] = map[string]any{
			"resourceType": "AllergyIntolerance",
			"id":           fmt.Sprintf("allergy-%d", i),
			"clinicalStatus": map[string]any{
				"coding": []any{map[string]any{"system": "http://terminology.hl7.org/CodeSystem/allergyintolerance-clinical", "code": "active"}},
			},
			"patient": map[string]any{"reference": fmt.Sprintf("Patient/patient-%d", i%15)},
		}
	}
	return out
}

// capabilityStatementJSON builds the synthetic CapabilityStatement served
// at /metadata. defective selects the Phase 1 acceptance-test fixture
// (brief §7); !defective is its "truthful" control twin — same shape and
// claims, but every planted lie is fixed in the corresponding handler.
func capabilityStatementJSON() map[string]any {
	resource := func(typ string, interactions []string, searchParams []map[string]any, includes, revIncludes []string) map[string]any {
		var ints []any
		for _, c := range interactions {
			ints = append(ints, map[string]any{"code": c})
		}
		var sps []any
		for _, sp := range searchParams {
			sps = append(sps, sp)
		}
		var incs, revs []any
		for _, i := range includes {
			incs = append(incs, i)
		}
		for _, r := range revIncludes {
			revs = append(revs, r)
		}
		return map[string]any{
			"type":             typ,
			"interaction":      ints,
			"searchParam":      sps,
			"searchInclude":    incs,
			"searchRevInclude": revs,
		}
	}
	sp := func(name, typ string) map[string]any {
		return map[string]any{"name": name, "type": typ, "definition": "http://hl7.org/fhir/SearchParameter/" + name}
	}

	return map[string]any{
		"resourceType": "CapabilityStatement",
		"fhirVersion":  "4.0.1",
		"software":     map[string]any{"name": "kweli-testserver", "version": "1.0.0"},
		"rest": []any{
			map[string]any{
				"mode": "server",
				"resource": []any{
					resource("Patient", []string{"search-type"}, []map[string]any{sp("birthdate", "date"), sp("name", "string"), sp("_lastUpdated", "date")}, nil, nil),
					resource("Observation", []string{"search-type"}, []map[string]any{sp("code", "token")}, nil, nil),
					resource("Condition", []string{"search-type"}, nil, []string{"Condition:subject"}, nil),
					resource("Encounter", []string{"search-type"}, nil, nil, nil),
					resource("Procedure", []string{"search-type"}, nil, nil, nil),
					resource("MedicationRequest", []string{"search-type", "read"}, nil, nil, nil),
					resource("AllergyIntolerance", []string{"search-type"}, []map[string]any{sp("clinical-status", "token")}, nil, nil),
					resource("Immunization", []string{"search-type"}, []map[string]any{sp("date", "date")}, nil, nil),
					resource("DiagnosticReport", []string{"search-type"}, []map[string]any{sp("based-on", "reference")}, nil, nil),
				},
			},
		},
	}
}
