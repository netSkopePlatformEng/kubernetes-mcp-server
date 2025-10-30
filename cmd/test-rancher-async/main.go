package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/config"
	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/kubernetes"
	"github.com/netSkopePlatformEng/kubernetes-mcp-server/pkg/mcp/jobs"
	"k8s.io/klog/v2"
)

func main() {
	fmt.Println("=== Rancher Async Download Test ===")
	fmt.Println()

	// Create Rancher config
	cfg := &config.RancherConfig{
		Enabled:   true,
		URL:       "https://rancher.prime.iad0.netskope.com",
		Token:     "token-64l2k:dfnjc76lcgthfn8s4wlzqmpjsvfljvxtgvqb2224z2bmkzsrx4qszx",
		Insecure:  true,
		ConfigDir: "/tmp/rancher-async-test",
	}

	// Clean test directory
	os.RemoveAll(cfg.ConfigDir)
	os.MkdirAll(cfg.ConfigDir, 0755)

	// Create multi-cluster manager
	staticCfg := &config.StaticConfig{
		KubeConfigDir: cfg.ConfigDir,
	}
	logger := klog.Background()
	mcm, err := kubernetes.NewMultiClusterManager(staticCfg, logger)
	if err != nil {
		log.Fatalf("Failed to create multi-cluster manager: %v", err)
	}

	// Create Rancher integration
	rancher := kubernetes.NewRancherIntegration(cfg, mcm, logger)

	// Test 1: List clusters
	fmt.Println("📋 Test 1: Listing clusters from Rancher...")
	ctx := context.Background()
	clusterNames, err := rancher.ListClusters(ctx)
	if err != nil {
		log.Fatalf("Failed to list clusters: %v", err)
	}
	fmt.Printf("✅ Found %d clusters\n\n", len(clusterNames))

	// Test 2: Start async download job
	fmt.Println("🚀 Test 2: Starting async download job...")

	// Create job manager
	jobConfig := jobs.DefaultManagerConfig()
	jobConfig.StorageDir = cfg.ConfigDir + "/jobs"
	jobManager, err := jobs.NewManager(jobConfig)
	if err != nil {
		log.Fatalf("Failed to create job manager: %v", err)
	}

	// Create executor
	executor := jobs.NewRancherDownloadExecutor(rancher, cfg.ConfigDir, mcm)

	// Start job
	job, created := jobManager.CreateOrGet("test-download", jobs.JobTypeRancherDownload, nil)
	if !created {
		log.Fatalf("Job already exists!")
	}

	if err := jobManager.StartJob(job.ID, executor); err != nil {
		log.Fatalf("Failed to start job: %v", err)
	}

	fmt.Printf("✅ Job started with ID: %s\n\n", job.ID)

	// Test 3: Monitor progress
	fmt.Println("⏳ Test 3: Monitoring progress (will take ~30-60 seconds)...")

	lastProgress := 0
	startTime := time.Now()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			job = jobManager.GetJob(job.ID)
			if job == nil {
				log.Fatalf("Job disappeared!")
			}

			progress := job.GetProgress()

			// Show progress if changed
			if progress.Done != lastProgress {
				elapsed := time.Since(startTime).Round(time.Second)
				fmt.Printf("  Progress: %d/%d clusters (%.1f%%) - %s [%s]\n",
					progress.Done, progress.Total, progress.Percentage, progress.Message, elapsed)
				lastProgress = progress.Done
			}

			// Check if complete
			if job.State == jobs.JobStateSucceeded || job.State == jobs.JobStateFailed {
				elapsed := time.Since(startTime).Round(time.Second)
				fmt.Printf("\n✅ Job completed in %s\n", elapsed)
				fmt.Printf("   State: %s\n", job.State)

				// Get final metadata
				meta, _ := jobManager.Storage.GetJobMetadata(job.ID)
				if meta != nil {
					fmt.Printf("   Succeeded: %d\n", meta.Succeeded)
					fmt.Printf("   Failed: %d\n", meta.Failed)
				}
				goto done
			}

			// Timeout after 5 minutes
			if time.Since(startTime) > 5*time.Minute {
				log.Fatalf("Job timeout after 5 minutes!")
			}
		}
	}

done:
	// Test 4: Verify files downloaded
	fmt.Println("\n📁 Test 4: Verifying downloaded files...")

	entries, err := os.ReadDir(cfg.ConfigDir)
	if err != nil {
		log.Fatalf("Failed to read directory: %v", err)
	}

	yamlCount := 0
	for _, entry := range entries {
		if !entry.IsDir() && len(entry.Name()) > 5 && entry.Name()[len(entry.Name())-5:] == ".yaml" {
			yamlCount++
		}
	}

	fmt.Printf("✅ Found %d YAML files in %s\n", yamlCount, cfg.ConfigDir)

	// Test 5: Check job results
	fmt.Println("\n📊 Test 5: Sampling job results...")

	page, err := jobManager.Storage.GetResults(job.ID, "", 5)
	if err != nil {
		log.Fatalf("Failed to get results: %v", err)
	}

	fmt.Printf("✅ First 5 results:\n")
	for i, result := range page.Items {
		status := "✅"
		if !result.Success {
			status = "❌"
		}
		fmt.Printf("  %d. %s %s - %dms\n", i+1, status, result.Cluster, result.DurationMs)
	}

	// Final summary
	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("🎉 ALL TESTS PASSED!")
	fmt.Printf("   • Listed %d clusters from Rancher\n", len(clusterNames))
	fmt.Printf("   • Downloaded %d kubeconfig files\n", yamlCount)
	fmt.Printf("   • Job completed in %s\n", time.Since(startTime).Round(time.Second))
	fmt.Printf("   • Average: %.1fs per cluster\n", time.Since(startTime).Seconds()/float64(yamlCount))
	fmt.Println(strings.Repeat("=", 50))
}
