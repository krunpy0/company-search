package agent

import (
	"math/big"
	"regexp"
)

const profitNotConfirmed = "Подтверждённых данных о чистой прибыли нет."

var (
	profitWords = regexp.MustCompile(`(?i)прибыл[ьиьюя]|убыт(?:ок|к|оч)`)
	profitTax   = regexp.MustCompile(`(?i)налог[а-яё]*\s+на\s+прибыль`)
	okvedLine   = regexp.MustCompile(`(?im)^([\t ]*(?:[-+*][\t ]+)?)[*_]*(?:(?:(?:основной\s+)?вид деятельности по|(?:основной\s+)?код)\s+)?(?:ОКВЭД|OKVED)[\t :*_]*([0-9]{2}(?:\.[0-9]{1,2}){0,2})(?:[^0-9.\n][^\n]*)?$`)
)

func mentionsProfit(text string) bool {
	return profitWords.MatchString(profitTax.ReplaceAllString(text, ""))
}

func revexpWithoutProfit(records map[string]Observation) bool {
	hasRevexp := false
	for _, record := range records {
		if record.Source["dataset"] == "finances" && record.Record["net_profit"] != nil {
			return false
		}
		hasRevexp = hasRevexp || record.Source["dataset"] == "revexp" && record.Record["income_expense_difference"] != nil
	}
	return hasRevexp
}

func formatOKVEDLines(text string, records map[string]Observation) string {
	return okvedLine.ReplaceAllStringFunc(text, func(line string) string {
		parts := okvedLine.FindStringSubmatch(line)
		code := parts[2]
		for _, ref := range citation.FindAllString(line, -1) {
			for _, record := range records {
				if record.Record["okved"] == code && record.SourceRef == ref {
					return parts[1] + "ОКВЭД " + code + " [" + ref + "]"
				}
			}
		}
		ref := ""
		for _, record := range records {
			if record.Record["okved"] == code && (ref == "" || record.SourceRef < ref) {
				ref = record.SourceRef
			}
		}
		if ref != "" {
			return parts[1] + "ОКВЭД " + code + " [" + ref + "]"
		}
		return parts[1] + "Код ОКВЭД не подтверждён."
	})
}

// Categories and subject types follow the published FNS SME record format.
// https://www.nalog.gov.ru/rn77/related_activities/regbusiness/#dev
var smeCategories = map[string]string{
	"1": "микропредприятие",
	"2": "малое предприятие",
	"3": "среднее предприятие",
}

var smeSubjects = map[string]string{
	"1": "юридическое лицо",
	"2": "индивидуальный предприниматель",
	"3": "глава крестьянского (фермерского) хозяйства",
}

// These are the normalized codes from the snr and snrip importers.
var regimeNames = map[string]string{
	"ausn": "Автоматизированная упрощённая система налогообложения (АвтоУСН)",
	"eshn": "Единый сельскохозяйственный налог (ЕСХН)",
	"npd":  "Налог на профессиональный доход (НПД)",
	"psn":  "Патентная система налогообложения (ПСН)",
	"srp":  "Система налогообложения при выполнении соглашений о разделе продукции (СРП)",
	"usn":  "Упрощённая система налогообложения (УСН)",
}

func addObservationFields(tool string, data map[string]any) {
	switch tool {
	case "get_company_profile":
		company, _ := data["company"].(map[string]any)
		sme, _ := company["sme"].(map[string]any)
		addLabel(sme, "category", "category_name", smeCategories)
		addLabel(sme, "subject_type", "subject_type_name", smeSubjects)
		addLabel(sme, "region_code", "region_name", regionNames)
	case "get_company_taxes":
		items, _ := data["items"].([]any)
		for _, value := range items {
			item, _ := value.(map[string]any)
			codes, _ := item["codes"].([]any)
			labels := make(map[string]string)
			for _, value := range codes {
				code, _ := value.(string)
				if label, ok := regimeNames[code]; ok {
					labels[code] = label
				}
			}
			if len(labels) > 0 {
				item["code_names"] = labels
			}
		}
	case "get_company_financials":
		items, _ := data["items"].([]any)
		for _, value := range items {
			item, _ := value.(map[string]any)
			income, _ := item["income"].(string)
			expense, _ := item["expense"].(string)
			incomeAmount, validIncome := new(big.Rat).SetString(income)
			expenseAmount, validExpense := new(big.Rat).SetString(expense)
			if validIncome && validExpense {
				item["income_expense_difference"] = incomeAmount.Sub(incomeAmount, expenseAmount).FloatString(2)
			}
		}
	}
}

func addLabel(record map[string]any, codeField, labelField string, labels map[string]string) {
	code, _ := record[codeField].(string)
	if label, ok := labels[code]; ok {
		record[labelField] = label
	}
}
