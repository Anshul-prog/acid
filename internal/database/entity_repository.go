// #file: entity_repository.go
// #package: database
// #purpose: All database operations for Entities, Cases, Work Sessions,
//           and Audit Logging.
//
// Key design decisions:
//   • maskPII flag controls whether doc_number / account_number are masked.
//     Only callers holding the "unmask" scope pass maskPII=false.
//   • pgtype.Date / pgtype.Timestamptz are used for nullable temporal columns
//     because pgx v5 does not automatically scan NULL into *time.Time.
//   • Queries use $1/$2/… parameters exclusively — no string interpolation of
//     user-supplied values.
//   • All writes append to entity_access_logs for forensic traceability.

package database

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"acid/internal/dbsearch"
)

// ─── Data-transfer objects ────────────────────────────────────────────────────

// Entity holds the base bio-data for a person / organisation / device.
// #entity-model: maps directly to the entities table.
type Entity struct {
	ID           string         `json:"id"`
	EntityType   string         `json:"entity_type"`
	FullName     string         `json:"full_name"`
	DisplayName  string         `json:"display_name,omitempty"`
	Alias        []string       `json:"alias,omitempty"`
	DOB          *time.Time     `json:"date_of_birth,omitempty"`
	Gender       string         `json:"gender,omitempty"`
	Nationality  string         `json:"nationality,omitempty"`
	Religion     string         `json:"religion,omitempty"`
	Occupation   string         `json:"occupation,omitempty"`
	PrimaryPhone string         `json:"primary_phone,omitempty"`
	PrimaryEmail string         `json:"primary_email,omitempty"`
	PhotoURL     string         `json:"photo_url,omitempty"`
	Meta         map[string]any `json:"meta,omitempty"`
	CreatedBy    string         `json:"created_by,omitempty"`
	UpdatedBy    string         `json:"updated_by,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// EntityAddress holds one address record (current / permanent / work …).
// #address-model
type EntityAddress struct {
	ID          string     `json:"id"`
	EntityID    string     `json:"entity_id"`
	AddressType string     `json:"address_type"`
	Line1       string     `json:"address_line1,omitempty"`
	Line2       string     `json:"address_line2,omitempty"`
	City        string     `json:"city,omitempty"`
	District    string     `json:"district,omitempty"`
	State       string     `json:"state,omitempty"`
	Pincode     string     `json:"pincode,omitempty"`
	Country     string     `json:"country,omitempty"`
	Landmark    string     `json:"landmark,omitempty"`
	IsVerified  bool       `json:"is_verified"`
	IsPrimary   bool       `json:"is_primary"`
	ValidFrom   time.Time  `json:"valid_from"`
	ValidTo     *time.Time `json:"valid_to,omitempty"`
}

// EntityDocument holds one identity document (Aadhaar, PAN, Passport …).
// #document-model: DocNumber is masked when maskPII == true.
type EntityDocument struct {
	ID          string     `json:"id"`
	EntityID    string     `json:"entity_id"`
	DocType     string     `json:"doc_type"`
	DocNumber   string     `json:"doc_number"`   // raw OR masked depending on scope
	IssuedBy    string     `json:"issued_by,omitempty"`
	IssuedDate  *time.Time `json:"issued_date,omitempty"`
	ExpiryDate  *time.Time `json:"expiry_date,omitempty"`
	ScanURL     string     `json:"scan_url,omitempty"`
	IsVerified  bool       `json:"is_verified"`
}

// EntityContact holds one contact entry (phone, email, WhatsApp …).
// #contact-model
type EntityContact struct {
	ID           string `json:"id"`
	EntityID     string `json:"entity_id"`
	ContactType  string `json:"contact_type"`
	ContactValue string `json:"contact_value"`
	Label        string `json:"label,omitempty"`
	IsPrimary    bool   `json:"is_primary"`
	IsVerified   bool   `json:"is_verified"`
}

// EntitySocialAccount holds one social media presence.
// #social-model
type EntitySocialAccount struct {
	ID              string `json:"id"`
	EntityID        string `json:"entity_id"`
	Platform        string `json:"platform"`
	Handle          string `json:"handle"`
	ProfileURL      string `json:"profile_url,omitempty"`
	FollowersCount  int64  `json:"followers_count,omitempty"`
	IsVerifiedAcct  bool   `json:"is_verified_account"`
	IsActive        bool   `json:"is_active"`
	Notes           string `json:"notes,omitempty"`
}

// EntityBankAccount holds one bank account.
// #bank-model: AccountNumber is masked when maskPII == true.
type EntityBankAccount struct {
	ID            string   `json:"id"`
	EntityID      string   `json:"entity_id"`
	AccountNumber string   `json:"account_number"` // raw OR masked
	BankName      string   `json:"bank_name,omitempty"`
	IFSCCode      string   `json:"ifsc_code,omitempty"`
	AccountType   string   `json:"account_type,omitempty"`
	BranchName    string   `json:"branch_name,omitempty"`
	UPIIDs        []string `json:"upi_ids,omitempty"`
	IsPrimary     bool     `json:"is_primary"`
}

// CaseRoleSummary summarises which case an entity is linked to and in what role.
// #case-role-model
type CaseRoleSummary struct {
	CaseID     string    `json:"case_id"`
	CaseNumber string    `json:"case_number"`
	Title      string    `json:"title"`
	Status     string    `json:"status"`
	Category   string    `json:"category,omitempty"`
	Role       string    `json:"role"`
	AddedAt    time.Time `json:"added_at"`
}

// EntityProfile aggregates all fragments of data for one entity.
// This is what GET /api/entities/{id}/profile returns.
// #profile-model
type EntityProfile struct {
	Entity         Entity                `json:"entity"`
	Addresses      []EntityAddress       `json:"addresses"`
	Documents      []EntityDocument      `json:"documents"`
	Contacts       []EntityContact       `json:"contacts"`
	SocialAccounts []EntitySocialAccount `json:"social_accounts"`
	BankAccounts   []EntityBankAccount   `json:"bank_accounts"`
	Cases          []CaseRoleSummary     `json:"cases"`
}

// Case holds investigation case metadata.
// #case-model
type Case struct {
	ID                   string     `json:"id"`
	CaseNumber           string     `json:"case_number"`
	Title                string     `json:"title"`
	Description          string     `json:"description,omitempty"`
	Status               string     `json:"status"`
	Category             string     `json:"category,omitempty"`
	SubCategory          string     `json:"sub_category,omitempty"`
	Jurisdiction         string     `json:"jurisdiction,omitempty"`
	InvestigatingOfficer string     `json:"investigating_officer,omitempty"`
	FIRNumber            string     `json:"fir_number,omitempty"`
	FIRDate              *time.Time `json:"fir_date,omitempty"`
	CourtName            string     `json:"court_name,omitempty"`
	Priority             string     `json:"priority"`
	CreatedBy            string     `json:"created_by,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// CaseDetail is a Case plus a list of linked entities (summaries only).
// #case-detail-model
type CaseDetail struct {
	Case     Case              `json:"case"`
	Entities []EntityWithRole  `json:"entities"`
}

// EntityWithRole is an entity summary with its role in a case.
// #entity-with-role-model
type EntityWithRole struct {
	EntityID     string    `json:"entity_id"`
	EntityType   string    `json:"entity_type"`
	FullName     string    `json:"full_name"`
	PrimaryPhone string    `json:"primary_phone,omitempty"`
	PrimaryEmail string    `json:"primary_email,omitempty"`
	PhotoURL     string    `json:"photo_url,omitempty"`
	Role         string    `json:"role"`
	RoleDesc     string    `json:"role_description,omitempty"`
	AddedAt      time.Time `json:"added_at"`
}

// WorkSession tracks investigator activity with start/end timestamps.
// #work-session-model
type WorkSession struct {
	ID          string     `json:"id"`
	UserID      string     `json:"user_id"`
	Username    string     `json:"username"`
	Description string     `json:"description,omitempty"`
	EntityID    *string    `json:"entity_id,omitempty"`
	CaseID      *string    `json:"case_id,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
}

// AccessLogEntry is one forensic audit record.
// #audit-model
type AccessLogEntry struct {
	UserID      string
	Username    string
	Action      string
	Scope       string
	QueryText   string
	EntityID    string
	CaseID      string
	DataSource  string
	ResultCount int
	IPAddress   string
	UserAgent   string
	SessionID   string
}

// ─── Repository ───────────────────────────────────────────────────────────────

// EntityRepository provides all DB operations for the cyber-cell domain.
// #repository: injected into entity and admin-search handlers.
type EntityRepository struct {
	pool *pgxpool.Pool
}

// NewEntityRepository creates a new EntityRepository backed by the given pool.
// #init: called in main.go after the pool is created.
func NewEntityRepository(pool *pgxpool.Pool) *EntityRepository {
	return &EntityRepository{pool: pool}
}

// ─── Entity profile ───────────────────────────────────────────────────────────

// GetEntityProfile fetches the full bio-data for one entity.
// maskPII==true → document numbers and bank accounts are masked.
//
// #profile-loader: executes 7 queries in parallel using goroutines to minimise latency.
func (r *EntityRepository) GetEntityProfile(ctx context.Context, entityID string, maskPII bool) (*EntityProfile, error) {
	// ── Base entity ──────────────────────────────────────────────────────────
	entity, err := r.getEntity(ctx, entityID)
	if err != nil {
		return nil, err
	}

	// ── Parallel fetch of all related data ───────────────────────────────────
	type result struct {
		addrs   []EntityAddress
		docs    []EntityDocument
		conts   []EntityContact
		social  []EntitySocialAccount
		banks   []EntityBankAccount
		cases   []CaseRoleSummary
		err     error
		which   int
	}

	ch := make(chan result, 6)

	go func() {
		v, e := r.getEntityAddresses(ctx, entityID); ch <- result{which: 1, addrs: v, err: e}
	}()
	go func() {
		v, e := r.getEntityDocuments(ctx, entityID, maskPII); ch <- result{which: 2, docs: v, err: e}
	}()
	go func() {
		v, e := r.getEntityContacts(ctx, entityID); ch <- result{which: 3, conts: v, err: e}
	}()
	go func() {
		v, e := r.getEntitySocial(ctx, entityID); ch <- result{which: 4, social: v, err: e}
	}()
	go func() {
		v, e := r.getEntityBankAccounts(ctx, entityID, maskPII); ch <- result{which: 5, banks: v, err: e}
	}()
	go func() {
		v, e := r.getEntityCases(ctx, entityID); ch <- result{which: 6, cases: v, err: e}
	}()

	profile := &EntityProfile{Entity: *entity}
	for i := 0; i < 6; i++ {
		res := <-ch
		if res.err != nil {
			return nil, fmt.Errorf("GetEntityProfile fetch %d: %w", res.which, res.err)
		}
		switch res.which {
		case 1:
			profile.Addresses = res.addrs
		case 2:
			profile.Documents = res.docs
		case 3:
			profile.Contacts = res.conts
		case 4:
			profile.SocialAccounts = res.social
		case 5:
			profile.BankAccounts = res.banks
		case 6:
			profile.Cases = res.cases
		}
	}
	return profile, nil
}

// getEntity fetches the base entity row.
func (r *EntityRepository) getEntity(ctx context.Context, id string) (*Entity, error) {
	const q = `
		SELECT id, entity_type, full_name, COALESCE(display_name,''),
		       alias, date_of_birth, COALESCE(gender,''), COALESCE(nationality,''),
		       COALESCE(religion,''), COALESCE(occupation,''),
		       COALESCE(primary_phone,''), COALESCE(primary_email,''),
		       COALESCE(photo_url,''), meta,
		       COALESCE(created_by,''), COALESCE(updated_by,''),
		       created_at, updated_at
		FROM entities
		WHERE id = $1 AND deleted_at IS NULL
	`
	row := r.pool.QueryRow(ctx, q, id)

	var e Entity
	var dob pgtype.Date
	var metaBytes []byte
	var alias []string

	if err := row.Scan(
		&e.ID, &e.EntityType, &e.FullName, &e.DisplayName,
		&alias, &dob, &e.Gender, &e.Nationality,
		&e.Religion, &e.Occupation,
		&e.PrimaryPhone, &e.PrimaryEmail,
		&e.PhotoURL, &metaBytes,
		&e.CreatedBy, &e.UpdatedBy,
		&e.CreatedAt, &e.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("entity %s not found: %w", id, err)
	}

	if dob.Valid {
		t := dob.Time
		e.DOB = &t
	}
	if len(alias) > 0 {
		e.Alias = alias
	}
	if metaBytes != nil {
		_ = json.Unmarshal(metaBytes, &e.Meta)
	}
	return &e, nil
}

func (r *EntityRepository) getEntityAddresses(ctx context.Context, entityID string) ([]EntityAddress, error) {
	const q = `
		SELECT id, address_type,
		       COALESCE(address_line1,''), COALESCE(address_line2,''),
		       COALESCE(city,''), COALESCE(district,''), COALESCE(state,''),
		       COALESCE(pincode,''), COALESCE(country,''), COALESCE(landmark,''),
		       is_verified, is_primary, valid_from, valid_to
		FROM entity_addresses
		WHERE entity_id = $1
		ORDER BY is_primary DESC, created_at DESC
	`
	rows, err := r.pool.Query(ctx, q, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EntityAddress
	for rows.Next() {
		var a EntityAddress
		var validTo pgtype.Timestamptz
		a.EntityID = entityID
		if err := rows.Scan(
			&a.ID, &a.AddressType,
			&a.Line1, &a.Line2,
			&a.City, &a.District, &a.State,
			&a.Pincode, &a.Country, &a.Landmark,
			&a.IsVerified, &a.IsPrimary, &a.ValidFrom, &validTo,
		); err != nil {
			continue
		}
		if validTo.Valid {
			t := validTo.Time
			a.ValidTo = &t
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *EntityRepository) getEntityDocuments(ctx context.Context, entityID string, maskPII bool) ([]EntityDocument, error) {
	const q = `
		SELECT id, doc_type, doc_number, doc_number_masked,
		       COALESCE(issued_by,''), issued_date, expiry_date,
		       COALESCE(scan_url,''), is_verified
		FROM entity_documents
		WHERE entity_id = $1
		ORDER BY created_at DESC
	`
	rows, err := r.pool.Query(ctx, q, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EntityDocument
	for rows.Next() {
		var d EntityDocument
		var maskedNum *string
		var issuedDate, expiryDate pgtype.Date
		d.EntityID = entityID

		if err := rows.Scan(
			&d.ID, &d.DocType, &d.DocNumber, &maskedNum,
			&d.IssuedBy, &issuedDate, &expiryDate,
			&d.ScanURL, &d.IsVerified,
		); err != nil {
			continue
		}

		// #pii-masking: apply masking unless caller has unmask scope
		if maskPII {
			if maskedNum != nil && *maskedNum != "" {
				d.DocNumber = *maskedNum
			} else {
				d.DocNumber = dbsearch.MaskDocNumber(d.DocType, d.DocNumber)
			}
		}

		if issuedDate.Valid {
			t := issuedDate.Time
			d.IssuedDate = &t
		}
		if expiryDate.Valid {
			t := expiryDate.Time
			d.ExpiryDate = &t
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *EntityRepository) getEntityContacts(ctx context.Context, entityID string) ([]EntityContact, error) {
	const q = `
		SELECT id, contact_type, contact_value, COALESCE(label,''), is_primary, is_verified
		FROM entity_contacts
		WHERE entity_id = $1
		ORDER BY is_primary DESC, created_at DESC
	`
	rows, err := r.pool.Query(ctx, q, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EntityContact
	for rows.Next() {
		var c EntityContact
		c.EntityID = entityID
		if err := rows.Scan(&c.ID, &c.ContactType, &c.ContactValue, &c.Label, &c.IsPrimary, &c.IsVerified); err != nil {
			continue
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *EntityRepository) getEntitySocial(ctx context.Context, entityID string) ([]EntitySocialAccount, error) {
	const q = `
		SELECT id, platform, handle, COALESCE(profile_url,''),
		       COALESCE(followers_count,0), is_verified_account, is_active, COALESCE(notes,'')
		FROM entity_social_accounts
		WHERE entity_id = $1
		ORDER BY platform, created_at DESC
	`
	rows, err := r.pool.Query(ctx, q, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EntitySocialAccount
	for rows.Next() {
		var s EntitySocialAccount
		s.EntityID = entityID
		if err := rows.Scan(&s.ID, &s.Platform, &s.Handle, &s.ProfileURL,
			&s.FollowersCount, &s.IsVerifiedAcct, &s.IsActive, &s.Notes); err != nil {
			continue
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *EntityRepository) getEntityBankAccounts(ctx context.Context, entityID string, maskPII bool) ([]EntityBankAccount, error) {
	const q = `
		SELECT id, COALESCE(account_number,''), COALESCE(account_number_masked,''),
		       COALESCE(bank_name,''), COALESCE(ifsc_code,''),
		       COALESCE(account_type,''), COALESCE(branch_name,''),
		       upi_ids, is_primary
		FROM entity_bank_accounts
		WHERE entity_id = $1
		ORDER BY is_primary DESC, created_at DESC
	`
	rows, err := r.pool.Query(ctx, q, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EntityBankAccount
	for rows.Next() {
		var b EntityBankAccount
		var maskedNum string
		var upiIDs []string
		b.EntityID = entityID

		if err := rows.Scan(&b.ID, &b.AccountNumber, &maskedNum,
			&b.BankName, &b.IFSCCode, &b.AccountType, &b.BranchName,
			&upiIDs, &b.IsPrimary); err != nil {
			continue
		}

		// #pii-masking
		if maskPII {
			if maskedNum != "" {
				b.AccountNumber = maskedNum
			} else {
				b.AccountNumber = dbsearch.MaskBankAccount(b.AccountNumber)
			}
		}
		b.UPIIDs = upiIDs
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *EntityRepository) getEntityCases(ctx context.Context, entityID string) ([]CaseRoleSummary, error) {
	const q = `
		SELECT c.id, c.case_number, c.title, c.status,
		       COALESCE(c.category,''), cer.role, cer.added_at
		FROM case_entity_roles cer
		JOIN cases c ON c.id = cer.case_id
		WHERE cer.entity_id = $1 AND c.deleted_at IS NULL
		ORDER BY cer.added_at DESC
	`
	rows, err := r.pool.Query(ctx, q, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CaseRoleSummary
	for rows.Next() {
		var cr CaseRoleSummary
		if err := rows.Scan(&cr.CaseID, &cr.CaseNumber, &cr.Title,
			&cr.Status, &cr.Category, &cr.Role, &cr.AddedAt); err != nil {
			continue
		}
		out = append(out, cr)
	}
	return out, rows.Err()
}

// ─── Cases ────────────────────────────────────────────────────────────────────

// ListCasesFilter holds optional filters for the cases list endpoint.
type ListCasesFilter struct {
	Status   string
	Category string
	Q        string
	Limit    int
	Offset   int
}

// ListCases returns cases matching the given filters.
// #case-list: used by GET /api/cases.
func (r *EntityRepository) ListCases(ctx context.Context, f ListCasesFilter) ([]Case, int, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}

	args := []any{}
	where := "deleted_at IS NULL"
	argN := 1

	if f.Status != "" {
		args = append(args, f.Status)
		where += fmt.Sprintf(" AND status = $%d", argN)
		argN++
	}
	if f.Category != "" {
		args = append(args, f.Category)
		where += fmt.Sprintf(" AND category = $%d", argN)
		argN++
	}
	if f.Q != "" {
		args = append(args, "%"+f.Q+"%")
		where += fmt.Sprintf(" AND (title ILIKE $%d OR case_number ILIKE $%d OR fir_number ILIKE $%d)", argN, argN, argN)
		argN++
	}

	countQ := fmt.Sprintf("SELECT COUNT(*) FROM cases WHERE %s", where)
	var total int
	if err := r.pool.QueryRow(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, f.Limit, f.Offset)
	dataQ := fmt.Sprintf(`
		SELECT id, case_number, title, COALESCE(description,''), status,
		       COALESCE(category,''), COALESCE(sub_category,''),
		       COALESCE(jurisdiction,''), COALESCE(investigating_officer,''),
		       COALESCE(fir_number,''), fir_date,
		       COALESCE(court_name,''), COALESCE(priority,'normal'),
		       COALESCE(created_by,''), created_at, updated_at
		FROM cases WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d
	`, where, argN, argN+1)

	rows, err := r.pool.Query(ctx, dataQ, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []Case
	for rows.Next() {
		var c Case
		var firDate pgtype.Date
		if err := rows.Scan(
			&c.ID, &c.CaseNumber, &c.Title, &c.Description, &c.Status,
			&c.Category, &c.SubCategory,
			&c.Jurisdiction, &c.InvestigatingOfficer,
			&c.FIRNumber, &firDate,
			&c.CourtName, &c.Priority,
			&c.CreatedBy, &c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			continue
		}
		if firDate.Valid {
			t := firDate.Time
			c.FIRDate = &t
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}

// GetCaseDetail returns one case with its linked entities.
// #case-detail: used by GET /api/cases/{id}.
func (r *EntityRepository) GetCaseDetail(ctx context.Context, caseID string) (*CaseDetail, error) {
	const caseQ = `
		SELECT id, case_number, title, COALESCE(description,''), status,
		       COALESCE(category,''), COALESCE(sub_category,''),
		       COALESCE(jurisdiction,''), COALESCE(investigating_officer,''),
		       COALESCE(fir_number,''), fir_date,
		       COALESCE(court_name,''), COALESCE(priority,'normal'),
		       COALESCE(created_by,''), created_at, updated_at
		FROM cases WHERE id = $1 AND deleted_at IS NULL
	`
	var c Case
	var firDate pgtype.Date
	row := r.pool.QueryRow(ctx, caseQ, caseID)
	if err := row.Scan(
		&c.ID, &c.CaseNumber, &c.Title, &c.Description, &c.Status,
		&c.Category, &c.SubCategory,
		&c.Jurisdiction, &c.InvestigatingOfficer,
		&c.FIRNumber, &firDate,
		&c.CourtName, &c.Priority,
		&c.CreatedBy, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("case %s not found: %w", caseID, err)
	}
	if firDate.Valid {
		t := firDate.Time
		c.FIRDate = &t
	}

	const entityQ = `
		SELECT e.id, e.entity_type, e.full_name,
		       COALESCE(e.primary_phone,''), COALESCE(e.primary_email,''),
		       COALESCE(e.photo_url,''),
		       cer.role, COALESCE(cer.role_description,''), cer.added_at
		FROM case_entity_roles cer
		JOIN entities e ON e.id = cer.entity_id
		WHERE cer.case_id = $1 AND e.deleted_at IS NULL
		ORDER BY cer.role, cer.added_at DESC
	`
	rows, err := r.pool.Query(ctx, entityQ, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entities []EntityWithRole
	for rows.Next() {
		var e EntityWithRole
		if err := rows.Scan(
			&e.EntityID, &e.EntityType, &e.FullName,
			&e.PrimaryPhone, &e.PrimaryEmail, &e.PhotoURL,
			&e.Role, &e.RoleDesc, &e.AddedAt,
		); err != nil {
			continue
		}
		entities = append(entities, e)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}

	return &CaseDetail{Case: c, Entities: entities}, nil
}

// ─── Work Sessions ────────────────────────────────────────────────────────────

// StartWorkSession inserts a new open work session and returns its ID.
// #work-session: called by POST /api/work-sessions.
func (r *EntityRepository) StartWorkSession(ctx context.Context, ws WorkSession) (string, error) {
	const q = `
		INSERT INTO work_sessions(user_id, username, description, entity_id, case_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`
	var eid, cid *string
	if ws.EntityID != nil && *ws.EntityID != "" {
		eid = ws.EntityID
	}
	if ws.CaseID != nil && *ws.CaseID != "" {
		cid = ws.CaseID
	}

	var id string
	err := r.pool.QueryRow(ctx, q, ws.UserID, ws.Username, ws.Description, eid, cid).Scan(&id)
	return id, err
}

// EndWorkSession sets ended_at on an open session.
// #work-session: called by PATCH /api/work-sessions/{id}/end.
func (r *EntityRepository) EndWorkSession(ctx context.Context, sessionID, userID string) error {
	const q = `
		UPDATE work_sessions
		SET ended_at = NOW()
		WHERE id = $1 AND user_id = $2 AND ended_at IS NULL
	`
	_, err := r.pool.Exec(ctx, q, sessionID, userID)
	return err
}

// ListWorkSessions returns recent sessions for a user.
func (r *EntityRepository) ListWorkSessions(ctx context.Context, userID string, limit int) ([]WorkSession, error) {
	const q = `
		SELECT id, user_id, username, COALESCE(description,''),
		       entity_id::text, case_id::text, started_at, ended_at
		FROM work_sessions
		WHERE user_id = $1
		ORDER BY started_at DESC
		LIMIT $2
	`
	rows, err := r.pool.Query(ctx, q, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []WorkSession
	for rows.Next() {
		var ws WorkSession
		var eid, cid *string
		var endedAt pgtype.Timestamptz
		if err := rows.Scan(
			&ws.ID, &ws.UserID, &ws.Username, &ws.Description,
			&eid, &cid, &ws.StartedAt, &endedAt,
		); err != nil {
			continue
		}
		ws.EntityID = eid
		ws.CaseID = cid
		if endedAt.Valid {
			t := endedAt.Time
			ws.EndedAt = &t
		}
		out = append(out, ws)
	}
	return out, rows.Err()
}

// ─── Forensic Audit ───────────────────────────────────────────────────────────

// LogAccess writes one immutable audit record to entity_access_logs.
// #forensic: every handler calls this; failures are logged but not propagated
//   so a log write failure never blocks the primary response.
func (r *EntityRepository) LogAccess(ctx context.Context, e AccessLogEntry) {
	const q = `
		INSERT INTO entity_access_logs
		(user_id, username, action, scope, query_text, entity_id, case_id,
		 data_source, result_count, ip_address, user_agent, session_id)
		VALUES ($1,$2,$3,$4,$5,
		        NULLIF($6,'')::uuid, NULLIF($7,'')::uuid,
		        $8,$9,$10,$11,$12)
	`
	_, err := r.pool.Exec(ctx, q,
		e.UserID, e.Username, e.Action, e.Scope, e.QueryText,
		e.EntityID, e.CaseID,
		e.DataSource, e.ResultCount, e.IPAddress, e.UserAgent, e.SessionID,
	)
	if err != nil {
		// Non-fatal — log to stderr only.
		fmt.Printf("[audit-log] write failed: %v\n", err)
	}
}

// ─── Export ───────────────────────────────────────────────────────────────────

// WriteProfileJSON serialises the profile as indented JSON to w.
// #export: used by the JSON download endpoint.
func WriteProfileJSON(w io.Writer, p *EntityProfile) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}

// WriteProfileCSV writes the entity profile as a flat CSV file to w.
// Multiple sections are separated by blank rows with a header comment.
// #export: used by the CSV download endpoint; works with Excel and LibreOffice.
func WriteProfileCSV(w io.Writer, p *EntityProfile) error {
	cw := csv.NewWriter(w)

	write := func(rows [][]string) {
		for _, r := range rows {
			_ = cw.Write(r)
		}
	}

	// ── Bio-data ──────────────────────────────────────────────────────────────
	write([][]string{
		{"=== BIO DATA ==="},
		{"Field", "Value"},
		{"ID", p.Entity.ID},
		{"Full Name", p.Entity.FullName},
		{"Display Name", p.Entity.DisplayName},
		{"Entity Type", p.Entity.EntityType},
		{"Gender", p.Entity.Gender},
		{"Nationality", p.Entity.Nationality},
		{"Religion", p.Entity.Religion},
		{"Occupation", p.Entity.Occupation},
		{"Primary Phone", p.Entity.PrimaryPhone},
		{"Primary Email", p.Entity.PrimaryEmail},
		{"Created At", p.Entity.CreatedAt.Format(time.RFC3339)},
		{"Updated At", p.Entity.UpdatedAt.Format(time.RFC3339)},
		{},
	})

	// ── Addresses ─────────────────────────────────────────────────────────────
	write([][]string{
		{"=== ADDRESSES ==="},
		{"Type", "Line1", "Line2", "City", "District", "State", "Pincode", "Country", "Landmark", "Verified", "Primary"},
	})
	for _, a := range p.Addresses {
		write([][]string{{
			a.AddressType, a.Line1, a.Line2, a.City, a.District, a.State,
			a.Pincode, a.Country, a.Landmark,
			boolStr(a.IsVerified), boolStr(a.IsPrimary),
		}})
	}
	write([][]string{{}})

	// ── Documents ─────────────────────────────────────────────────────────────
	write([][]string{
		{"=== IDENTITY DOCUMENTS ==="},
		{"Type", "Number", "Issued By", "Verified"},
	})
	for _, d := range p.Documents {
		write([][]string{{d.DocType, d.DocNumber, d.IssuedBy, boolStr(d.IsVerified)}})
	}
	write([][]string{{}})

	// ── Contacts ──────────────────────────────────────────────────────────────
	write([][]string{
		{"=== CONTACTS ==="},
		{"Type", "Value", "Label", "Primary", "Verified"},
	})
	for _, c := range p.Contacts {
		write([][]string{{c.ContactType, c.ContactValue, c.Label, boolStr(c.IsPrimary), boolStr(c.IsVerified)}})
	}
	write([][]string{{}})

	// ── Social accounts ───────────────────────────────────────────────────────
	write([][]string{
		{"=== SOCIAL ACCOUNTS ==="},
		{"Platform", "Handle", "Profile URL", "Followers", "Verified"},
	})
	for _, s := range p.SocialAccounts {
		write([][]string{{
			s.Platform, s.Handle, s.ProfileURL,
			fmt.Sprintf("%d", s.FollowersCount),
			boolStr(s.IsVerifiedAcct),
		}})
	}
	write([][]string{{}})

	// ── Bank accounts ─────────────────────────────────────────────────────────
	write([][]string{
		{"=== BANK ACCOUNTS ==="},
		{"Account Number", "Bank", "IFSC", "Type", "Branch", "Primary"},
	})
	for _, b := range p.BankAccounts {
		write([][]string{{
			b.AccountNumber, b.BankName, b.IFSCCode,
			b.AccountType, b.BranchName, boolStr(b.IsPrimary),
		}})
	}
	write([][]string{{}})

	// ── Cases ─────────────────────────────────────────────────────────────────
	write([][]string{
		{"=== CASES ==="},
		{"Case Number", "Title", "Status", "Category", "Role", "Added At"},
	})
	for _, c := range p.Cases {
		write([][]string{{
			c.CaseNumber, c.Title, c.Status, c.Category, c.Role,
			c.AddedAt.Format(time.RFC3339),
		}})
	}

	cw.Flush()
	return cw.Error()
}

func boolStr(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}
