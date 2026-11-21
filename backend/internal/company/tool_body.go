package company

// ToolBody extends the shared read result with tool provenance and state reasons.
func (r Result) ToolBody() Body {
	body := r.Body
	if body == nil {
		return nil
	}
	const providerRef = "dadata"
	var providerURL string
	mark := func(o *registrationObservation) {
		o.SourceRef = providerRef
		providerURL = o.SourceURL
	}
	if o, ok := body["dadata"].(registrationObservation); ok {
		mark(&o)
		body["dadata"] = o
	}
	if items, ok := body["items"].([]searchItem); ok {
		for i := range items {
			if items[i].DaData != nil {
				mark(items[i].DaData)
			}
		}
	}
	if observations, ok := body["external_suggestions"].([]registrationObservation); ok {
		for i := range observations {
			mark(&observations[i])
		}
	}
	if providerURL != "" {
		sources, ok := body["sources"].(Body)
		if !ok {
			sources = make(Body)
			for ref, source := range body["sources"].(map[string]Source) {
				sources[ref] = source
			}
			body["sources"] = sources
		}
		sources[providerRef] = Body{"publisher": "DaData", "dataset": "registration", "url": providerURL}
	}
	addStateReason(body)
	if sections, ok := body["sections"].(Body); ok {
		for _, section := range sections {
			addStateReason(section.(Body))
		}
	}
	return body
}

func addStateReason(body Body) {
	var reason string
	switch body["state"] {
	case "not_published":
		reason = "No matching observation in the checked release or source"
	case "not_applicable":
		reason = "This dataset does not apply to this subject type"
	case "not_loaded":
		reason = "The requested release or source was not loaded or checked"
	case "unavailable":
		reason = "The source is temporarily unavailable"
	case "identity_conflict":
		reason = "The source observation has conflicting identifiers"
	}
	if reason != "" {
		body["reason"] = reason
	}
}
