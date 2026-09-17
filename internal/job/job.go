package job

import (
	"encoding/csv"
	"os"
	"strconv"
)

// JobPosting is one structured job record extracted from a single HN comment.
type JobPosting struct {
	Location            string
	JobTitle            string
	Description         string
	HowToApply          string
	SalaryActual        string
	SalaryMinAmount     float64
	SalaryMaxAmount     float64
	SalaryCurrencyCode  string
	SalaryNormalizedUSD float64
	HasSalary           bool
	HasNormalizedUSD    bool
}

var csvHeader = []string{
	"location",
	"job_title",
	"description",
	"how_to_apply",
	"salary_actual",
	"salary_min_amount",
	"salary_max_amount",
	"salary_currency_code",
	"salary_normalized_usd",
}

// WriteCSV writes jobs to path as CSV with csvHeader as the header row.
// Salary fields are written as empty strings, not "0", when a job has no
// stated salary (JobPosting.HasSalary == false).
func WriteCSV(path string, jobs []JobPosting) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write(csvHeader); err != nil {
		return err
	}

	for _, j := range jobs {
		if err := w.Write(j.row()); err != nil {
			return err
		}
	}

	w.Flush()
	return w.Error()
}

func (j JobPosting) row() []string {
	if !j.HasSalary {
		return []string{j.Location, j.JobTitle, j.Description, j.HowToApply, j.SalaryActual, "", "", "", ""}
	}
	normalizedUSD := ""
	if j.HasNormalizedUSD {
		normalizedUSD = formatFloat(j.SalaryNormalizedUSD)
	}
	return []string{
		j.Location,
		j.JobTitle,
		j.Description,
		j.HowToApply,
		j.SalaryActual,
		formatFloat(j.SalaryMinAmount),
		formatFloat(j.SalaryMaxAmount),
		j.SalaryCurrencyCode,
		normalizedUSD,
	}
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
