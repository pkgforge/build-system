package metadata

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// GHCRPackage represents a package from GHCR
type GHCRPackage struct {
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
	UpdatedAt  string `json:"updated_at"`
	URL        string `json:"url"`
}

// GraphQL types for package query
type graphQLRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables"`
}

type graphQLResponse struct {
	Data struct {
		Organization struct {
			Packages struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []struct {
					Name       string `json:"name"`
					Visibility string `json:"visibility"`
					UpdatedAt  string `json:"updatedAt"`
				} `json:"nodes"`
			} `json:"packages"`
		} `json:"organization"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// FetchGHCRPackages fetches all public packages from GitHub Container Registry
// using GraphQL API to bypass the 10k REST API limit
func FetchGHCRPackages() ([]GHCRPackage, error) {
	fmt.Println("Querying GitHub GraphQL API for pkgforge GHCR packages...")

	token := getGitHubToken()
	if token == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN environment variable is required to query org packages")
	}

	const graphqlQuery = `
		query($cursor: String, $perPage: Int!) {
			organization(login: "pkgforge") {
				packages(first: $perPage, after: $cursor, packageType: CONTAINER) {
					pageInfo {
						hasNextPage
						endCursor
					}
					nodes {
						name
						visibility
						updatedAt
					}
				}
			}
		}
	`

	var allPackages []GHCRPackage
	cursor := ""
	perPage := 100
	maxRetries := 3
	pageCount := 0

	for {
		pageCount++
		var lastErr error
		var gqlResp graphQLResponse

		// Retry logic for transient errors
		for attempt := 1; attempt <= maxRetries; attempt++ {
			reqBody := graphQLRequest{
				Query: graphqlQuery,
				Variables: map[string]interface{}{
					"perPage": perPage,
				},
			}

			if cursor != "" {
				reqBody.Variables["cursor"] = cursor
			}

			jsonBody, err := json.Marshal(reqBody)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal GraphQL request: %w", err)
			}

			req, err := http.NewRequest("POST", "https://api.github.com/graphql", bytes.NewBuffer(jsonBody))
			if err != nil {
				return nil, fmt.Errorf("failed to create request: %w", err)
			}

			req.Header.Set("Authorization", "bearer "+token)
			req.Header.Set("Content-Type", "application/json")

			client := &http.Client{Timeout: 30 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				lastErr = fmt.Errorf("failed to fetch GHCR packages (attempt %d/%d): %w", attempt, maxRetries, err)
				if attempt < maxRetries {
					time.Sleep(time.Duration(attempt) * time.Second)
					continue
				}
				return nil, lastErr
			}

			// Read and close body immediately to avoid resource leaks
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()

			if err != nil {
				lastErr = fmt.Errorf("failed to read response body (attempt %d/%d): %w", attempt, maxRetries, err)
				if attempt < maxRetries {
					time.Sleep(time.Duration(attempt) * time.Second)
					continue
				}
				return nil, lastErr
			}

			// Handle non-200 status codes with retry for server errors
			if resp.StatusCode != http.StatusOK {
				lastErr = fmt.Errorf("failed to fetch GHCR packages: status %d, body: %s", resp.StatusCode, string(body))
				// Retry on 5xx errors (server-side issues)
				if resp.StatusCode >= 500 && resp.StatusCode < 600 && attempt < maxRetries {
					fmt.Printf("  Server error (status %d), retrying in %d seconds... (attempt %d/%d)\n",
						resp.StatusCode, attempt, attempt, maxRetries)
					time.Sleep(time.Duration(attempt*2) * time.Second)
					continue
				}
				return nil, lastErr
			}

			// Parse GraphQL response
			if err := json.Unmarshal(body, &gqlResp); err != nil {
				return nil, fmt.Errorf("failed to parse GraphQL response: %w", err)
			}

			// Check for GraphQL errors
			if len(gqlResp.Errors) > 0 {
				return nil, fmt.Errorf("GraphQL errors: %v", gqlResp.Errors)
			}

			// Success - break out of retry loop
			break
		}

		// Convert GraphQL nodes to GHCRPackage
		for _, node := range gqlResp.Data.Organization.Packages.Nodes {
			allPackages = append(allPackages, GHCRPackage{
				Name:       node.Name,
				Visibility: node.Visibility,
				UpdatedAt:  node.UpdatedAt,
			})
		}

		fmt.Printf("  Fetched page %d (%d packages so far)\n", pageCount, len(allPackages))

		// Check if there are more pages
		if !gqlResp.Data.Organization.Packages.PageInfo.HasNextPage {
			break
		}

		cursor = gqlResp.Data.Organization.Packages.PageInfo.EndCursor

		// Add a small delay between requests to avoid rate limiting
		time.Sleep(100 * time.Millisecond)
	}

	fmt.Printf("Completed: Fetched %d total packages across %d pages\n", len(allPackages), pageCount)
	return allPackages, nil
}

// getGitHubToken retrieves GitHub token from environment
func getGitHubToken() string {
	// Try common environment variable names
	if token := os.Getenv("GHCR_TOKEN"); token != "" {
		return token
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token
	}
	if token := os.Getenv("GH_TOKEN"); token != "" {
		return token
	}
	return ""
}

// FilterBincachePackages filters GHCR packages for bincache
func FilterBincachePackages(packages []GHCRPackage) []string {
	var result []string

	for _, pkg := range packages {
		// Only public packages
		if pkg.Visibility != "public" {
			continue
		}

		// Filter for bincache packages (not srcbuild)
		if strings.Contains(pkg.Name, "-srcbuild") {
			continue
		}

		if strings.Contains(pkg.Name, "bincache") {
			result = append(result, pkg.Name)
		}
	}

	return result
}

// FilterPkgcachePackages filters GHCR packages for pkgcache
func FilterPkgcachePackages(packages []GHCRPackage) []string {
	var result []string

	for _, pkg := range packages {
		// Only public packages
		if pkg.Visibility != "public" {
			continue
		}

		// Filter for pkgcache packages
		if strings.Contains(pkg.Name, "pkgcache") {
			result = append(result, pkg.Name)
		}
	}

	return result
}
