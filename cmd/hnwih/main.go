package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/gusdecool/hacker-news-who-ishiring-scrapper/internal/cli"
	"github.com/gusdecool/hacker-news-who-ishiring-scrapper/internal/extract"
	"github.com/gusdecool/hacker-news-who-ishiring-scrapper/internal/fx"
	"github.com/gusdecool/hacker-news-who-ishiring-scrapper/internal/hn"
)

func main() {
	var urlFlag, outFlag, geminiAPIKeyFlag string
	var concurrency int

	rootCmd := &cobra.Command{
		Use:   "hnwih",
		Short: `Scrape a Hacker News "Who is hiring?" thread into structured CSV`,
		RunE: func(cmd *cobra.Command, args []string) error {
			geminiAPIKey := geminiAPIKeyFlag
			if geminiAPIKey == "" {
				geminiAPIKey = os.Getenv("GEMINI_API_KEY")
			}

			extractor, err := extract.ResolveExtractor(extract.Config{GeminiAPIKey: geminiAPIKey})
			if err != nil {
				return err
			}

			httpClient := &http.Client{Timeout: 30 * time.Second}
			hnClient := hn.NewAlgoliaClient(httpClient)
			converter := fx.NewFrankfurterConverter(httpClient)

			ctx := context.Background()
			if err := converter.FetchRates(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not fetch FX rates, salary normalization disabled: %v\n", err)
			}

			_, err = cli.Run(ctx, cli.Config{
				ThreadURL:   urlFlag,
				OutPath:     outFlag,
				Concurrency: concurrency,
			}, cli.Deps{
				HN:        hnClient,
				Extractor: extractor,
				Converter: converter,
			}, os.Stderr)
			return err
		},
	}

	rootCmd.Flags().StringVar(&urlFlag, "url", "", "HN thread URL or numeric ID (required)")
	rootCmd.Flags().StringVar(&outFlag, "out", "jobs.csv", "output CSV path")
	rootCmd.Flags().StringVar(&geminiAPIKeyFlag, "gemini-api-key", "", "Gemini API key (or set GEMINI_API_KEY)")
	rootCmd.Flags().IntVar(&concurrency, "concurrency", 5, "number of comments processed concurrently")
	_ = rootCmd.MarkFlagRequired("url")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
