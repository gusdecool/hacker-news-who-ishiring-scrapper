package job

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCSV_WithSalary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.csv")

	jobs := []JobPosting{
		{
			Location:            "Remote (Europe)",
			JobTitle:            "Senior Product Engineer",
			Description:         "Build cool things",
			HowToApply:          "https://modash.io",
			SalaryActual:        "€75k-110k",
			SalaryMinAmount:     75000,
			SalaryMaxAmount:     110000,
			SalaryCurrencyCode:  "EUR",
			SalaryNormalizedUSD: 106500.5,
			HasSalary:           true,
		},
	}

	if err := WriteCSV(path, jobs); err != nil {
		t.Fatalf("WriteCSV returned error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}

	want := "location,job_title,description,how_to_apply,salary_actual,salary_min_amount,salary_max_amount,salary_currency_code,salary_normalized_usd\n" +
		"Remote (Europe),Senior Product Engineer,Build cool things,https://modash.io,€75k-110k,75000,110000,EUR,106500.5\n"

	if string(got) != want {
		t.Fatalf("CSV content mismatch\ngot:  %q\nwant: %q", string(got), want)
	}
}

func TestWriteCSV_WithoutSalary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.csv")

	jobs := []JobPosting{
		{
			Location:    "On-site San Francisco",
			JobTitle:    "Backend Engineer",
			Description: "No salary mentioned",
			HowToApply:  "jobs@example.com",
			HasSalary:   false,
		},
	}

	if err := WriteCSV(path, jobs); err != nil {
		t.Fatalf("WriteCSV returned error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}

	want := "location,job_title,description,how_to_apply,salary_actual,salary_min_amount,salary_max_amount,salary_currency_code,salary_normalized_usd\n" +
		"On-site San Francisco,Backend Engineer,No salary mentioned,jobs@example.com,,,,,\n"

	if string(got) != want {
		t.Fatalf("CSV content mismatch\ngot:  %q\nwant: %q", string(got), want)
	}
}

func TestWriteCSV_EscapesSpecialCharacters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.csv")

	jobs := []JobPosting{
		{
			Location:    "Remote",
			JobTitle:    "Engineer, Platform",
			Description: "Line one\nLine two",
			HowToApply:  "apply@example.com",
			HasSalary:   false,
		},
	}

	if err := WriteCSV(path, jobs); err != nil {
		t.Fatalf("WriteCSV returned error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}

	want := "location,job_title,description,how_to_apply,salary_actual,salary_min_amount,salary_max_amount,salary_currency_code,salary_normalized_usd\n" +
		"Remote,\"Engineer, Platform\",\"Line one\nLine two\",apply@example.com,,,,,\n"

	if string(got) != want {
		t.Fatalf("CSV content mismatch\ngot:  %q\nwant: %q", string(got), want)
	}
}
