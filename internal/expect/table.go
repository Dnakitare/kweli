// Package expect implements brief Phase 3's "--expect us-core" mode: a
// fourth finding class, "missing", for a search param (or whole resource
// type) US Core 6.1 requires that the target server's CapabilityStatement
// never claims at all. This is a static comparison against a table, not a
// probe — no network request is involved, per the brief's own "Just a
// table; no IG parsing" scope.
package expect

// USCoreRequiredParams maps a US Core 6.1 resource type to the search
// params a conformant server's CapabilityStatement must claim.
//
// Source: US Core 6.1.0's own reference CapabilityStatement —
// http://hl7.org/fhir/us/core/STU6.1/CapabilityStatement-us-core-server.json
// — not hand-transcribed from the narrative spec. Every resource type here
// has both a resource-level and a search-type interaction "expectation"
// extension of SHALL in that document; the param list is the union of:
//   - search params individually marked SHALL, and
//   - params referenced inside a SHALL-level
//     capabilitystatement-search-parameter-combination extension.
//
// That second bullet matters more than it looks: most US Core resources
// (Observation, CarePlan, MedicationRequest, ...) mark every *individual*
// search param only MAY or SHOULD — the actual mandatory requirement is
// almost always a *combination* ("patient+category", "patient+code", ...).
// Using only individually-SHALL params would make this table nearly
// useless for exactly the resources people care about most; Observation
// alone would report zero required params despite US Core mandating
// patient+category and patient+code search.
//
// What this table deliberately does NOT check (the "no IG parsing" line):
// whether a server actually supports searching by those params *together*
// — only whether the CapabilityStatement claims each constituent param at
// all. A server whose CapabilityStatement claims Observation search by
// both "patient" and "category" satisfies this check even if it silently
// ignores a combined patient+category query; internal/probe's own
// per-param two-query test (§5.3) is what would catch that, separately,
// if the server had claimed the params kweli tested them against.
//
// Resources US Core lists but excludes from this table (their own
// resource-level or search-type expectation isn't SHALL, so a server
// isn't required to support searching them at all): Endpoint,
// HealthcareService, Media, Medication, Provenance, Questionnaire,
// QuestionnaireResponse, ServiceRequest, Specimen, ValueSet.
var USCoreRequiredParams = map[string][]string{
	"AllergyIntolerance": {"patient"},
	"CarePlan":           {"category", "patient"},
	"CareTeam":           {"patient", "status"},
	"Condition":          {"category", "patient"},
	"Coverage":           {"patient"},
	"Device":             {"patient"},
	"DiagnosticReport":   {"category", "code", "date", "patient"},
	"DocumentReference":  {"_id", "category", "date", "patient", "type"},
	"Encounter":          {"_id", "date", "patient"},
	"Goal":               {"patient"},
	"Immunization":       {"patient"},
	"Location":           {"address", "name"},
	"MedicationDispense": {"patient"},
	"MedicationRequest":  {"intent", "patient", "status"},
	"Observation":        {"category", "code", "date", "patient"},
	"Organization":       {"address", "name"},
	"Patient":            {"_id", "birthdate", "gender", "identifier", "name"},
	"Practitioner":       {"identifier", "name"},
	"PractitionerRole":   {"practitioner", "specialty"},
	"Procedure":          {"date", "patient"},
	"RelatedPerson":      {"_id"},
}
