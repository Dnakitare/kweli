package sample

// Paths is the curated (resourceType, searchParam) -> dotted JSON path
// table from brief §5.3 step 1. It lets kweli find a real value to search
// for without a FHIRPath engine.
//
// Path syntax: dot-separated segments. A segment that is all digits indexes
// into an array (so "coding.0" means "first element of the coding array").
// A path resolves to either a plain scalar (string/number/bool) or a small
// object (e.g. a Coding {system, code}) that FormatSearchValue knows how to
// render for a given FHIR search param type.
//
// Params not listed here fall through to the nonsense-only query (§5.3
// step 3) — that's deliberate for some (e.g. DiagnosticReport's
// "based-on", see brief §7 defect 12) and just incompleteness for others.
// Grow this table; it's the whole point of avoiding a FHIRPath engine.
var Paths = map[string]string{
	// Patient
	"Patient/birthdate":            "birthDate",
	"Patient/family":               "name.0.family",
	"Patient/given":                "name.0.given.0",
	"Patient/name":                 "name.0.family",
	"Patient/gender":               "gender",
	"Patient/active":               "active",
	"Patient/identifier":           "identifier.0.value",
	"Patient/address-city":         "address.0.city",
	"Patient/address-state":        "address.0.state",
	"Patient/address-postalcode":   "address.0.postalCode",
	"Patient/general-practitioner": "generalPractitioner.0.reference",
	"Patient/organization":         "managingOrganization.reference",
	"Patient/_id":                  "id",
	"Patient/_lastUpdated":         "meta.lastUpdated",

	// Observation
	"Observation/code":           "code.coding.0",
	"Observation/category":       "category.0.coding.0",
	"Observation/status":         "status",
	"Observation/patient":        "subject.reference",
	"Observation/subject":        "subject.reference",
	"Observation/encounter":      "encounter.reference",
	"Observation/date":           "effectiveDateTime",
	"Observation/value-quantity": "valueQuantity.value",
	"Observation/value-concept":  "valueCodeableConcept.coding.0",
	"Observation/component-code": "component.0.code.coding.0",
	"Observation/identifier":     "identifier.0.value",
	"Observation/_id":            "id",
	"Observation/_lastUpdated":   "meta.lastUpdated",

	// Condition
	"Condition/patient":             "subject.reference",
	"Condition/subject":             "subject.reference",
	"Condition/code":                "code.coding.0",
	"Condition/clinical-status":     "clinicalStatus.coding.0",
	"Condition/verification-status": "verificationStatus.coding.0",
	"Condition/onset-date":          "onsetDateTime",
	"Condition/encounter":           "encounter.reference",
	"Condition/identifier":          "identifier.0.value",

	// Encounter
	"Encounter/patient":    "subject.reference",
	"Encounter/subject":    "subject.reference",
	"Encounter/status":     "status",
	"Encounter/class":      "class.code",
	"Encounter/date":       "period.start",
	"Encounter/type":       "type.0.coding.0",
	"Encounter/identifier": "identifier.0.value",

	// Procedure
	"Procedure/patient": "subject.reference",
	"Procedure/subject": "subject.reference",
	"Procedure/code":    "code.coding.0",
	"Procedure/date":    "performedDateTime",
	"Procedure/status":  "status",

	// MedicationRequest
	"MedicationRequest/patient":    "subject.reference",
	"MedicationRequest/subject":    "subject.reference",
	"MedicationRequest/status":     "status",
	"MedicationRequest/intent":     "intent",
	"MedicationRequest/medication": "medicationCodeableConcept.coding.0",
	"MedicationRequest/authoredon": "authoredOn",

	// AllergyIntolerance
	"AllergyIntolerance/patient":         "patient.reference",
	"AllergyIntolerance/clinical-status": "clinicalStatus.coding.0",
	"AllergyIntolerance/code":            "code.coding.0",

	// Immunization
	"Immunization/patient":      "patient.reference",
	"Immunization/date":         "occurrenceDateTime",
	"Immunization/status":       "status",
	"Immunization/vaccine-code": "vaccineCode.coding.0",

	// DiagnosticReport (deliberately no "based-on" entry — see brief §7.12)
	"DiagnosticReport/patient":  "subject.reference",
	"DiagnosticReport/subject":  "subject.reference",
	"DiagnosticReport/code":     "code.coding.0",
	"DiagnosticReport/status":   "status",
	"DiagnosticReport/date":     "effectiveDateTime",
	"DiagnosticReport/category": "category.0.coding.0",

	// Practitioner
	"Practitioner/name":       "name.0.family",
	"Practitioner/identifier": "identifier.0.value",
	"Practitioner/_id":        "id",

	// Organization
	"Organization/name":       "name",
	"Organization/identifier": "identifier.0.value",
}
