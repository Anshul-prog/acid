package main

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const (
	NumDatabases   = 10
	NumTablesPerDB = 1000
	UsersPerTable  = 50
	Workers        = 10
)

var (
	firstNames = []string{"James", "Mary", "John", "Patricia", "Robert", "Jennifer", "Michael", "Linda", "William", "Elizabeth", "David", "Barbara", "Richard", "Susan", "Joseph", "Jessica", "Thomas", "Sarah", "Charles", "Karen", "Christopher", "Nancy", "Daniel", "Lisa", "Matthew", "Betty", "Anthony", "Margaret", "Mark", "Sandra", "Donald", "Ashley", "Steven", "Kimberly", "Paul", "Emily", "Andrew", "Donna", "Joshua", "Michelle", "Kenneth", "Dorothy", "Kevin", "Carol", "Brian", "Amanda", "George", "Melissa", "Timothy", "Deborah"}
	lastNames  = []string{"Smith", "Johnson", "Williams", "Brown", "Jones", "Garcia", "Miller", "Davis", "Rodriguez", "Martinez", "Hernandez", "Lopez", "Gonzalez", "Wilson", "Anderson", "Thomas", "Taylor", "Moore", "Jackson", "Martin", "Lee", "Perez", "Thompson", "White", "Harris", "Sanchez", "Clark", "Ramirez", "Lewis", "Robinson", "Walker", "Young", "Allen", "King", "Wright", "Scott", "Torres", "Nguyen", "Hill", "Flores", "Green", "Adams", "Nelson", "Baker", "Hall", "Rivera", "Campbell", "Mitchell", "Carter", "Roberts"}
	domains    = []string{"gmail.com", "yahoo.com", "hotmail.com", "outlook.com", "proton.me", "icloud.com"}
	statuses   = []string{"active", "inactive", "pending", "suspended", "verified"}
	countries  = []string{"US", "UK", "CA", "AU", "DE", "FR", "JP", "IN", "BR", "MX"}
	cities     = []string{"New York", "Los Angeles", "Chicago", "Houston", "Phoenix", "Philadelphia", "San Antonio", "San Diego", "Dallas", "San Jose", "Austin", "Jacksonville", "Fort Worth", "Columbus", "Charlotte", "Seattle", "Denver", "Boston", "Nashville", "Portland"}
	domains2   = []string{"finance", "health", "education", "technology", "retail", "manufacturing", "media", "transport", "energy", "construction"}
	jobTitles  = []string{"Manager", "Developer", "Engineer", "Director", "Analyst", "Consultant", "Coordinator", "Administrator", "Specialist", "Executive"}
)

type TableSchema struct {
	Name        string
	Columns     []ColumnDef
	PrimaryKeys []string
}

type ColumnDef struct {
	Name     string
	Type     string
	IsUnique bool
}

type GeneratedData struct {
	Database string
	Table    string
	RecordID int
	Users    []UserRecord
}

type UserRecord struct {
	ID             int64     `json:"id"`
	UUID           string    `json:"uuid"`
	Email          string    `json:"email"`
	FirstName      string    `json:"first_name"`
	LastName       string    `json:"last_name"`
	Phone          string    `json:"phone"`
	Status         string    `json:"status"`
	Country        string    `json:"country"`
	City           string    `json:"city"`
	Domain         string    `json:"domain"`
	JobTitle       string    `json:"job_title"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Tags           []string  `json:"tags,omitempty"`
	DuplicateDB    string    `json:"duplicate_db,omitempty"`
	DuplicateTable string    `json:"duplicate_table,omitempty"`
}

func main() {
	ctx := context.Background()

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("╔═══════════════════════════════════════════════════════════")
	log.Println("║ L.S.D Multi-Database Sample Data Generator")
	log.Println("║ Generating 10 databases × 1000 tables × 50 users each")
	log.Println("╚═══════════════════════════════════════════════════════════")

	baseDir := getProjectRoot()
	databasesDir := filepath.Join(baseDir, "databases")

	if err := os.MkdirAll(databasesDir, 0755); err != nil {
		log.Fatalf("Failed to create databases dir: %v", err)
	}

	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://postgres:password@localhost:5432/lsd?sslmode=disable"
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("Database not reachable: %v", err)
	}

	startTime := time.Now()

	for dbIdx := 1; dbIdx <= NumDatabases; dbIdx++ {
		dbName := fmt.Sprintf("lsd_db_%02d", dbIdx)

		log.Printf("📦 Creating database: %s", dbName)

		if err := createDatabase(ctx, pool, dbName); err != nil {
			log.Printf("⚠️  Database exists or error: %v", err)
		}

		var wg sync.WaitGroup
		chunkSize := NumTablesPerDB / Workers

		for w := 0; w < Workers; w++ {
			wg.Add(1)
			startTable := w * chunkSize
			endTable := startTable + chunkSize
			if w == Workers-1 {
				endTable = NumTablesPerDB
			}

			go func(start, end int) {
				defer wg.Done()
				for t := start; t < end; t++ {
					tableName := fmt.Sprintf("users_%04d", t)
					if err := generateTableData(ctx, pool, dbName, tableName, UsersPerTable, dbIdx, t); err != nil {
						log.Printf("Error generating table %s: %v", tableName, err)
					}
				}
			}(startTable, endTable)
		}

		wg.Wait()
		log.Printf("✅ Database %s completed: %d tables created", dbName, NumTablesPerDB)
	}

	duration := time.Since(startTime)
	log.Printf("🎉 All databases created in %v", duration)
	log.Printf("📊 Total: %d databases × %d tables × %d users = %d total records",
		NumDatabases, NumTablesPerDB, UsersPerTable, NumDatabases*NumTablesPerDB*UsersPerTable)

	metaFile := filepath.Join(databasesDir, "metadata.json")
	writeMetadata(metaFile)

	pdfReportPath := filepath.Join(baseDir, "reports", "sample_data_report.csv")
	if err := generateReportCSV(ctx, pool, pdfReportPath); err != nil {
		log.Printf("⚠️  CSV report error: %v", err)
	} else {
		log.Printf("📄 Report saved to: %s", pdfReportPath)
	}
}

func getProjectRoot() string {
	wd, _ := os.Getwd()
	if filepath.Base(wd) == "databases" || filepath.Base(wd) == "scripts" {
		return filepath.Dir(wd)
	}
	return wd
}

func createDatabase(ctx context.Context, pool *pgxpool.Pool, dbName string) error {
	_, err := pool.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE", dbName))
	if err != nil {
		return err
	}

	_, err = pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", dbName))
	return err
}

func generateTableData(ctx context.Context, pool *pgxpool.Pool, dbName, tableName string, numRecords, dbIdx, tableIdx int) error {
	tableFullyQualified := fmt.Sprintf("%s.%s", dbName, tableName)

	_, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id BIGSERIAL PRIMARY KEY,
			uuid VARCHAR(36) UNIQUE NOT NULL,
			email VARCHAR(255) UNIQUE NOT NULL,
			first_name VARCHAR(100) NOT NULL,
			last_name VARCHAR(100) NOT NULL,
			phone VARCHAR(20),
			status VARCHAR(20) DEFAULT 'active',
			country VARCHAR(2),
			city VARCHAR(100),
			domain VARCHAR(100),
			job_title VARCHAR(100),
			password_hash VARCHAR(255),
			is_active BOOLEAN DEFAULT true,
			tags TEXT[],
			duplicate_ref TEXT,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`, tableFullyQualified))
	if err != nil {
		return err
	}

	_, err = pool.Exec(ctx, fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS idx_%s_email ON %s(email);
		CREATE INDEX IF NOT EXISTS idx_%s_status ON %s(status);
		CREATE INDEX IF NOT EXISTS idx_%s_country ON %s(country);
		CREATE INDEX IF NOT EXISTS idx_%s_domain ON %s(domain);
	`, tableName, tableFullyQualified, tableName, tableFullyQualified, tableName, tableFullyQualified, tableName, tableFullyQualified))
	if err != nil {
		return err
	}

	existingCount := 0
	pool.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", tableFullyQualified)).Scan(&existingCount)
	if existingCount >= numRecords {
		return nil
	}

	batch := make([]UserRecord, numRecords)
	duplicateChance := 0.15

	for i := 0; i < numRecords; i++ {
		firstName := firstNames[randInt(len(firstNames))]
		lastName := lastNames[randInt(len(lastNames))]
		email := generateEmail(firstName, lastName, dbIdx, tableIdx, i)

		record := UserRecord{
			ID:        int64(i + 1),
			UUID:      generateUUID(),
			Email:     email,
			FirstName: firstName,
			LastName:  lastName,
			Phone:     generatePhone(),
			Status:    statuses[randInt(len(statuses))],
			Country:   countries[randInt(len(countries))],
			City:      cities[randInt(len(cities))],
			Domain:    domains2[randInt(len(domains2))],
			JobTitle:  jobTitles[randInt(len(jobTitles))],
			CreatedAt: time.Now().Add(-time.Duration(randInt(365*24)) * time.Hour),
			UpdatedAt: time.Now(),
		}

		if randFloat() < duplicateChance {
			refDB := randInt(NumDatabases) + 1
			refTbl := randInt(NumTablesPerDB)
			record.Tags = []string{fmt.Sprintf("duplicate_ref:lsd_db_%02d.users_%04d", refDB, refTbl)}
			record.DuplicateDB = fmt.Sprintf("lsd_db_%02d", refDB)
			record.DuplicateTable = fmt.Sprintf("users_%04d", refTbl)
		}

		batch[i] = record
	}

	for _, record := range batch {
		passHash, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)

		_, err := pool.Exec(ctx, fmt.Sprintf(`
			INSERT INTO %s (uuid, email, first_name, last_name, phone, status, country, city, domain, job_title, password_hash, is_active, tags, duplicate_ref, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		`, tableFullyQualified),
			record.UUID, record.Email, record.FirstName, record.LastName, record.Phone,
			record.Status, record.Country, record.City, record.Domain, record.JobTitle,
			string(passHash), true, record.Tags, record.DuplicateDB, record.CreatedAt, record.UpdatedAt)

		if err != nil {
			log.Printf("Insert error: %v", err)
		}
	}

	return nil
}

func generateEmail(firstName, lastName string, dbIdx, tableIdx, recordIdx int) string {
	domain := domains[randInt(len(domains))]
	unique := fmt.Sprintf("%s%d.%s%d", firstName[:1], recordIdx, lastName[:1], dbIdx)
	return strings.ToLower(unique) + "@" + domain
}

func generateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func generatePhone() string {
	return fmt.Sprintf("+1-%03d-%03d-%04d",
		randRange(200, 999),
		randRange(100, 999),
		randRange(1000, 9999))
}

func randInt(max int) int {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(max)))
	return int(n.Int64())
}

func randRange(min, max int) int {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(max-min)))
	return min + int(n.Int64())
}

func randFloat() float64 {
	n, _ := rand.Int(rand.Reader, big.NewInt(10000))
	return float64(n.Int64()) / 10000.0
}

func writeMetadata(path string) {
	content := fmt.Sprintf(`{
	"generated_at": "%s",
	"generator": "L.S.D Multi-Database Generator",
	"version": "1.0.0",
	"databases": %d,
	"tables_per_db": %d,
	"users_per_table": %d,
	"total_records": %d,
	"structure": {
		"columns": ["id", "uuid", "email", "first_name", "last_name", "phone", "status", "country", "city", "domain", "job_title", "password_hash", "is_active", "tags", "duplicate_ref", "created_at", "updated_at"],
		"indexes": ["email", "status", "country", "domain"],
		"primary_key": "id"
	}
}`, time.Now().Format(time.RFC3339), NumDatabases, NumTablesPerDB, UsersPerTable, NumDatabases*NumTablesPerDB*UsersPerTable)

	os.WriteFile(path, []byte(content), 0644)
}

func generateReportCSV(ctx context.Context, pool *pgxpool.Pool, path string) error {
	os.MkdirAll(filepath.Dir(path), 0755)

	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	writer.Comma = ','

	headers := []string{"database", "table", "record_id", "email", "first_name", "last_name", "status", "country", "city", "tags", "duplicate_ref"}
	writer.Write(headers)

	for dbIdx := 1; dbIdx <= NumDatabases; dbIdx++ {
		dbName := fmt.Sprintf("lsd_db_%02d", dbIdx)

		for tIdx := 0; tIdx < NumTablesPerDB; tIdx++ {
			if tIdx >= 10 {
				break
			}

			tableName := fmt.Sprintf("users_%04d", tIdx)
			tableFullyQualified := fmt.Sprintf("%s.%s", dbName, tableName)

			rows, err := pool.Query(ctx, fmt.Sprintf("SELECT id, email, first_name, last_name, status, country, city, tags, duplicate_ref FROM %s LIMIT 100", tableFullyQualified))
			if err != nil {
				continue
			}

			for rows.Next() {
				var id int64
				var email, firstName, lastName, status, country, city string
				var tags []string
				var duplicateRef *string

				if err := rows.Scan(&id, &email, &firstName, &lastName, &status, &country, &city, &tags, &duplicateRef); err != nil {
					continue
				}

				tagsStr := ""
				if len(tags) > 0 {
					tagsStr = strings.Join(tags, ";")
				}

				dupRef := ""
				if duplicateRef != nil {
					dupRef = *duplicateRef
				}

				record := []string{dbName, tableName, strconv.FormatInt(id, 10), email, firstName, lastName, status, country, city, tagsStr, dupRef}
				writer.Write(record)
			}
			rows.Close()
		}
	}

	writer.Flush()
	return writer.Error()
}
