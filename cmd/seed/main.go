// Command seed creates the tables and a bench of demo engineers. Local and
// demo environments only.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"

	"github.com/AlfawakhryDev/hyrde-api/internal/env"
	"github.com/AlfawakhryDev/hyrde-api/internal/match"
	"github.com/AlfawakhryDev/hyrde-api/internal/store"
)

var skills = []string{"Procore", "QuickBooks", "AppFolio", "Yardi", "Buildium", "Sage Intacct", "Autodesk Construction Cloud", "DocuSign", "Stripe", "Plaid", "Receipt OCR", "Salesforce"}

func main() {
	n := flag.Int("engineers", 2000, "how many demo engineers to create")
	flag.Parse()
	ctx := context.Background()

	db, err := store.New(ctx, env.String("AWS_REGION", "us-east-1"), env.String("DYNAMODB_ENDPOINT", "http://localhost:8000"), env.String("TABLE_PREFIX", "hyrde_"))
	if err != nil {
		log.Fatal(err)
	}
	if err := db.EnsureTables(ctx); err != nil {
		log.Fatal(err)
	}
	r := rand.New(rand.NewPCG(42, 42)) // fixed seed: the same bench every run
	for i := range *n {                // range over an int: Go 1.22+
		e := match.Engineer{
			ID: fmt.Sprintf("eng-%05d", i), Name: fmt.Sprintf("Engineer %d", i),
			Status: "approved", HourlyRateUSD: 30 + r.IntN(70), MaxConcurrent: 1 + r.IntN(3),
		}
		for range 1 + r.IntN(3) {
			e.Skills = append(e.Skills, skills[r.IntN(len(skills))])
		}
		if r.IntN(10) == 0 {
			e.Status = "trial"
		}
		if err := db.PutEngineer(ctx, e); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Printf("seeded %d engineers\n", *n)
}
