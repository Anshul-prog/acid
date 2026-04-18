let currentTable = '';
let currentCursor = '';
let cursorHistory = [];
let currentPage = 1;
let isSearchMode = false;
let currentSearchTerm = '';
let tableSchema = null;
let searchableColumns = [];
let clickhouseAvailable = false;

async function loadTables() {
    try {
        const response = await fetch('/api/tables');
        const data = await response.json();
        
        document.getElementById('total-tables').textContent = data.count;
        
        const select = document.getElementById('table-select');
        select.innerHTML = '<option value="">-- Select a table to view records --</option>';
        
        for (const table of data.tables) {
            const option = document.createElement('option');
            option.value = table.name;
            option.textContent = `${table.name} (${table.columns} columns)`;
            select.appendChild(option);
        }
        
    } catch (error) {
        console.error('Error loading tables:', error);
    } finally {
        await Promise.allSettled([
            loadHealthStatus(),
            loadCDCStatus(),
        ]);
    }
}

async function loadHealthStatus() {
    try {
        const response = await fetch('/api/health');
        const health = await response.json();
        
        clickhouseAvailable = health.clickhouse || false;
        
        const redisStatus = document.getElementById('redis-status');
        redisStatus.textContent = health.redis ? 'Connected' : 'Not Available';
        redisStatus.className = 'status-badge ' + (health.redis ? 'status-ok' : 'status-off');
        
        const chStatus = document.getElementById('clickhouse-status');
        chStatus.textContent = health.clickhouse ? 'Connected' : 'Not Available';
        chStatus.className = 'status-badge ' + (health.clickhouse ? 'status-ok' : 'status-off');
    } catch (error) {
        console.error('Error loading health status:', error);
    }
}

async function loadCDCStatus() {
    try {
        const response = await fetch('/api/cdc/status');
        const cdc = await response.json();
        
        const cdcStatus = document.getElementById('cdc-status');
        const cdcTables = document.getElementById('cdc-tables');
        
        if (cdc.is_running) {
            cdcStatus.textContent = 'Running';
            cdcStatus.className = 'status-badge status-ok';
            cdcTables.textContent = `(${cdc.total_tables} tables)`;
        } else {
            cdcStatus.textContent = 'Stopped';
            cdcStatus.className = 'status-badge status-off';
            cdcTables.textContent = '';
        }
    } catch (error) {
        console.error('Error loading CDC status:', error);
        const cdcStatus = document.getElementById('cdc-status');
        cdcStatus.textContent = 'Unknown';
        cdcStatus.className = 'status-badge status-unknown';
    }
}

async function selectTable() {
    const select = document.getElementById('table-select');
    currentTable = select.value;
    
    // Hide global results when selecting a table
    document.getElementById('global-results').style.display = 'none';
    document.getElementById('table-container').style.display = 'block';
    
    if (!currentTable) {
        document.getElementById('current-table').textContent = '-';
        document.getElementById('total-records').textContent = '-';
        document.getElementById('total-columns').textContent = '-';
        return;
    }
    
    document.getElementById('current-table').textContent = currentTable;
    cursorHistory = [];
    currentCursor = '';
    currentPage = 1;
    isSearchMode = false;
    
    await loadTableSchema();
    await loadTableStats();
    await loadRecords();
}

async function loadTableSchema() {
    try {
        const response = await fetch(`/api/tables/${currentTable}/schema`);
        tableSchema = await response.json();
        
        document.getElementById('total-columns').textContent = tableSchema.columns.length;
        
        searchableColumns = tableSchema.searchable || [];
        
        const sortSelect = document.getElementById('sort-by');
        sortSelect.innerHTML = '<option value="">Default</option>';
        
        for (const col of tableSchema.sortable || []) {
            const option = document.createElement('option');
            option.value = col;
            option.textContent = col;
            sortSelect.appendChild(option);
        }
        
        const thead = document.getElementById('table-head');
        thead.innerHTML = '';
        const headerRow = document.createElement('tr');
        
        for (const col of tableSchema.columns) {
            const th = document.createElement('th');
            th.textContent = col.name;
            const small = document.createElement('small');
            small.textContent = col.type;
            th.appendChild(small);
            headerRow.appendChild(th);
        }
        
        thead.appendChild(headerRow);
        
        document.getElementById('sort-controls').style.display = 'flex';
        document.getElementById('search-options').style.display = 'flex';
    } catch (error) {
        console.error('Error loading table schema:', error);
    }
}

async function loadTableStats() {
    try {
        const response = await fetch(`/api/tables/${currentTable}/stats`);
        const stats = await response.json();
        
        const totalRecords = stats.total_records || stats.count || 0;
        document.getElementById('total-records').textContent = totalRecords.toLocaleString();
    } catch (error) {
        console.error('Error loading table stats:', error);
        document.getElementById('total-records').textContent = 'Error';
    }
}

async function loadRecords() {
    if (!currentTable) return;
    
    try {
        const sortBy = document.getElementById('sort-by').value;
        const sortOrder = document.getElementById('sort-order').value;
        
        let url = `/api/tables/${currentTable}/records?limit=20`;
        if (currentCursor) url += `&cursor=${currentCursor}`;
        if (sortBy) url += `&sort_by=${sortBy}&sort_dir=${sortOrder}`;
        
        const response = await fetch(url);
        const data = await response.json();
        
        displayRecords(data.data || data.records || []);
        
        document.getElementById('prev-btn').disabled = currentPage === 1;
        document.getElementById('next-btn').disabled = !data.next_cursor;
        document.getElementById('page-info').textContent = `Page ${currentPage} (${data.count || data.data?.length || 0} records)`;
        
        if (data.next_cursor) {
            currentCursor = data.next_cursor;
        }
    } catch (error) {
        console.error('Error loading records:', error);
    }
}

async function searchRecords() {
    const searchTerm = document.getElementById('search-input').value.trim();
    
    if (!searchTerm || !currentTable) {
        await loadRecords();
        return;
    }
    
    const engine = document.querySelector('input[name="engine"]:checked')?.value || 'auto';
    const caseSensitive = document.getElementById('case-sensitive')?.checked || false;
    
    try {
        const startTime = performance.now();
        let url = `/api/tables/${currentTable}/search?q=${encodeURIComponent(searchTerm)}&engine=${engine}&limit=20`;
        if (caseSensitive) url += '&case_sensitive=true';
        
        const response = await fetch(url);
        const data = await response.json();
        const endTime = performance.now();
        
        displayRecords(data.results || data.data || []);
        
        const searchSpeed = document.getElementById('search-speed');
        const time = (endTime - startTime).toFixed(2);
        const engineUsed = data.search_engine || data.engine || engine || 'unknown';
        searchSpeed.innerHTML = `Search completed in <span class="${engineUsed === 'clickhouse' ? 'search-fast' : 'search-pg'}">${time}ms</span> using <strong>${engineUsed}</strong>`;
        
        document.getElementById('page-info').textContent = `${data.count || data.results?.length || 0} results found`;
        document.getElementById('prev-btn').disabled = true;
        document.getElementById('next-btn').disabled = true;
        
        isSearchMode = true;
        currentSearchTerm = searchTerm;
    } catch (error) {
        console.error('Error searching records:', error);
    }
}

// ✅ NEW: GLOBAL SEARCH FUNCTION
// ✅ FIXED: GLOBAL SEARCH FUNCTION with better error handling
async function performGlobalSearch() {
    const searchTerm = document.getElementById('global-search-input').value.trim();
    
    if (!searchTerm) {
        alert('Please enter a search term');
        return;
    }
    
    const globalResults = document.getElementById('global-results');
    const tableContainer = document.getElementById('table-container');
    
    globalResults.style.display = 'block';
    tableContainer.style.display = 'none';
    globalResults.innerHTML = '<p style="text-align:center;color:#888;padding:2rem;">🔍 Searching across all tables...</p>';
    
    try {
        const startTime = performance.now();
        const response = await fetch(`/api/search?q=${encodeURIComponent(searchTerm)}`);
        
        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`);
        }
        
        const data = await response.json();
        const endTime = performance.now();
        const time = (endTime - startTime).toFixed(2);
        
        // 🔍 DEBUG: Log the response
        console.log('Global Search Response:', data);
        
        // ✅ Handle different response formats
        let results = {};
        let totalResults = 0;
        let tablesSearched = 0;
        
        // Format 1: {results: {table1: [...], table2: [...]}, total_results: N}
        if (data.results && typeof data.results === 'object') {
            results = data.results;
            totalResults = data.total_results || 0;
            tablesSearched = data.tables_searched || Object.keys(results).length;
        }
        // Format 2: {data: {table1: [...], table2: [...]}}
        else if (data.data && typeof data.data === 'object') {
            results = data.data;
            for (const table in results) {
                if (Array.isArray(results[table])) {
                    totalResults += results[table].length;
                }
            }
            tablesSearched = Object.keys(results).length;
        }
        // Format 3: Direct {table1: [...], table2: [...]}
        else {
            // Remove metadata fields
            const metadataFields = ['total_results', 'tables_searched', 'search_term', 'timestamp', 'engine'];
            results = {...data};
            metadataFields.forEach(field => delete results[field]);
            
            for (const table in results) {
                if (Array.isArray(results[table])) {
                    totalResults += results[table].length;
                }
            }
            tablesSearched = Object.keys(results).length;
            totalResults = data.total_results || totalResults;
            tablesSearched = data.tables_searched || tablesSearched;
        }
        
        // ✅ Build HTML
        let html = `
            <div style="background: rgba(255,255,255,0.03); border: 1px solid rgba(255,255,255,0.1); border-radius: 12px; padding: 1.5rem; margin-bottom: 2rem;">
                <h2 style="color: #00d9ff; margin-bottom: 0.5rem;">🌐 Global Search Results</h2>
                <p style="color: #888;">
                    Found <strong style="color: #00d9ff;">${totalResults}</strong> results 
                    across <strong style="color: #00d9ff;">${tablesSearched}</strong> tables 
                    in <strong style="color: #00ff00;">${time}ms</strong>
                </p>
            </div>
        `;
        
        // ✅ Display results by table
        let hasResults = false;
        
        for (const [tableName, tableResults] of Object.entries(results)) {
            if (!Array.isArray(tableResults) || tableResults.length === 0) {
                continue;
            }
            
            hasResults = true;
            
            html += `
                <div class="table-container" style="margin-bottom: 2rem;">
                    <h3 style="color: #00d9ff; margin-bottom: 1rem; padding: 0 1rem;">
                        📋 Table: <strong>${tableName}</strong>
                        <span style="color: #888; font-size: 0.9rem; font-weight: normal;">(${tableResults.length} results)</span>
                    </h3>
                    <div style="overflow-x: auto;">
                        <table>
                            <thead><tr>`;
            
            // Get column names from first row
            if (tableResults.length > 0) {
                const cols = Object.keys(tableResults[0]);
                cols.forEach(col => {
                    html += `<th>${col.toUpperCase()}</th>`;
                });
                html += '</tr></thead><tbody>';
                
                // Display rows (max 10 per table)
                tableResults.slice(0, 10).forEach(row => {
                    html += '<tr>';
                    cols.forEach(col => {
                        const val = row[col];
                        let displayVal;
                        
                        if (val === null || val === undefined) {
                            displayVal = '<em style="color:#666;">NULL</em>';
                        } else if (typeof val === 'object') {
                            displayVal = JSON.stringify(val);
                        } else {
                            // Highlight search term
                            displayVal = String(val);
                            const regex = new RegExp(`(${searchTerm})`, 'gi');
                            displayVal = displayVal.replace(regex, '<mark style="background:#00d9ff;color:#000;padding:0 2px;border-radius:2px;">$1</mark>');
                        }
                        
                        html += `<td>${displayVal}</td>`;
                    });
                    html += '</tr>';
                });
                
                if (tableResults.length > 10) {
                    html += `<tr><td colspan="${cols.length}" style="text-align:center;color:#888;padding:1rem;">
                        ... and ${tableResults.length - 10} more results
                    </td></tr>`;
                }
            }
            
            html += '</tbody></table></div></div>';
        }
        
        if (!hasResults) {
            html += `
                <div style="text-align:center;padding:3rem;">
                    <div style="font-size:3rem;margin-bottom:1rem;">🔍</div>
                    <p style="color:#888;font-size:1.2rem;">No results found for "${searchTerm}"</p>
                    <p style="color:#666;font-size:0.9rem;margin-top:0.5rem;">Try a different search term</p>
                </div>
            `;
        }
        
        globalResults.innerHTML = html;
        
    } catch (error) {
        console.error('Global search error:', error);
        globalResults.innerHTML = `
            <div style="background:rgba(255,82,82,0.1);border:1px solid rgba(255,82,82,0.3);border-radius:12px;padding:2rem;text-align:center;">
                <div style="font-size:2rem;margin-bottom:1rem;">⚠️</div>
                <p style="color:#ff5252;font-size:1.1rem;margin-bottom:0.5rem;">Search Failed</p>
                <p style="color:#888;font-size:0.9rem;">${error.message}</p>
                <button onclick="performGlobalSearch()" style="margin-top:1rem;padding:0.5rem 1rem;background:#00d9ff;border:none;border-radius:6px;color:#000;cursor:pointer;">
                    Try Again
                </button>
            </div>
        `;
    }
}


function displayRecords(rows) {
    const tbody = document.getElementById('table-body');
    tbody.innerHTML = '';
    
    if (!rows || rows.length === 0) {
        const row = document.createElement('tr');
        const cell = document.createElement('td');
        cell.colSpan = tableSchema?.columns.length || 1;
        cell.textContent = 'No records found';
        cell.style.textAlign = 'center';
        cell.style.padding = '2rem';
        cell.style.color = '#666';
        row.appendChild(cell);
        tbody.appendChild(row);
        return;
    }
    
    for (const record of rows) {
        const row = document.createElement('tr');
        
        for (const col of tableSchema.columns) {
            const cell = document.createElement('td');
            const value = record[col.name];
            
            if (value === null || value === undefined) {
                cell.innerHTML = '<em style="color:#666;">NULL</em>';
            } else if (typeof value === 'object') {
                cell.textContent = JSON.stringify(value);
            } else {
                cell.textContent = String(value);
            }
            
            cell.title = String(value);
            row.appendChild(cell);
        }
        
        tbody.appendChild(row);
    }
}

function nextPage() {
    if (isSearchMode) return;
    
    cursorHistory.push(currentCursor);
    currentPage++;
    loadRecords();
}

function prevPage() {
    if (currentPage === 1 || isSearchMode) return;
    
    cursorHistory.pop();
    currentCursor = cursorHistory[cursorHistory.length - 1] || '';
    currentPage--;
    loadRecords();
}

// Initialize on page load
window.addEventListener('DOMContentLoaded', loadTables);

// ═══════════════════════════════════════════════════════════════════════════
// INTELLIGENCE CONSOLE  —  L.S.D  frontend module
// #module: all functions below handle the new tabs added to dashboard.html.
// They share the same JWT/cookie auth used by the rest of the dashboard.
// ═══════════════════════════════════════════════════════════════════════════

// ── Tab switching ────────────────────────────────────────────────────────────
// #tab-switch: shows the selected panel, hides others, updates active tab style.
function intelTab(name) {
    document.querySelectorAll('.intel-panel').forEach(p => p.style.display = 'none');
    document.querySelectorAll('.intel-tab').forEach(t => t.classList.remove('active'));
    const panel = document.getElementById('panel-' + name);
    if (panel) panel.style.display = '';
    event.currentTarget.classList.add('active');
    // Lazy-load data on first open
    if (name === 'cases')    loadCases();
    if (name === 'sessions') loadWorkSessions();
}

// ── Helpers ──────────────────────────────────────────────────────────────────
function esc(s) {
    if (s === null || s === undefined) return '';
    return String(s)
        .replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;')
        .replace(/"/g,'&quot;');
}

function highlightTerm(text, term) {
    if (!term) return esc(text);
    const re = new RegExp('(' + term.replace(/[.*+?^${}()|[\]\\]/g,'\\$&') + ')', 'gi');
    return esc(String(text)).replace(re, '<mark class="hl">$1</mark>');
}

function statusBadge(s) {
    const cls = 'status-' + (s || 'unknown').replace(/ /g,'_');
    return `<span class="status-badge ${cls}">${esc(s)}</span>`;
}

function fmtDate(iso) {
    if (!iso) return '—';
    try { return new Date(iso).toLocaleString(); } catch { return iso; }
}

// ── DB Search Console ─────────────────────────────────────────────────────────
// #db-search: calls GET /api/admin/db-search with scope, source, schema, table.
async function runDBSearch() {
    const q      = document.getElementById('db-search-q').value.trim();
    const scope  = document.getElementById('db-scope').value;
    const ds     = document.getElementById('db-source-input').value.trim();
    const schema = document.getElementById('db-schema-input').value.trim();
    const table  = document.getElementById('db-table-input').value.trim();

    if (!q) { alert('Enter a search term'); return; }

    const status  = document.getElementById('db-search-status');
    const results = document.getElementById('db-search-results');
    status.textContent  = '⏳ Searching …';
    results.innerHTML   = '';

    const params = new URLSearchParams({ q, scope });
    if (ds)     params.set('dataSourceId', ds);
    if (schema) params.set('schema', schema);
    if (table)  params.set('table', table);

    const t0 = performance.now();
    try {
        const res  = await fetch('/api/admin/db-search?' + params);
        const data = await res.json();
        const ms   = (performance.now() - t0).toFixed(0);

        if (!res.ok) {
            status.textContent = '❌ ' + (data.error || res.statusText);
            return;
        }

        status.innerHTML = `✅ <strong>${data.total}</strong> hits &nbsp;·&nbsp; 
            <span class="qtype-pill">${data.query_type}</span> &nbsp;·&nbsp; ${ms} ms`;

        results.innerHTML = renderSearchResults(data.results, data.scope, q);
    } catch(e) {
        status.textContent = '❌ ' + e.message;
    }
}

// Trigger metadata refresh (admin only).
// #refresh: calls POST /api/admin/db-search/refresh
async function dbSearchRefresh() {
    const btn = event.currentTarget;
    btn.disabled = true;
    btn.textContent = '…';
    try {
        const res  = await fetch('/api/admin/db-search/refresh', { method: 'POST' });
        const data = await res.json();
        document.getElementById('db-search-status').textContent =
            data.ok ? '✅ Schema refreshed' : '❌ ' + (data.error || 'refresh failed');
    } catch(e) {
        document.getElementById('db-search-status').textContent = '❌ ' + e.message;
    } finally {
        btn.disabled = false;
        btn.textContent = '↺';
    }
}

// ── Smart Search ─────────────────────────────────────────────────────────────
// #smart-search: calls GET /api/smart-search — backend auto-classifies input.
async function runSmartSearch() {
    const q     = document.getElementById('smart-q').value.trim();
    const scope = document.getElementById('smart-scope').value;

    if (!q) { alert('Enter a search term'); return; }

    const status  = document.getElementById('smart-search-status');
    const results = document.getElementById('smart-search-results');
    status.textContent = '⏳ Searching …';
    results.innerHTML  = '';

    const t0 = performance.now();
    try {
        const res  = await fetch(`/api/smart-search?q=${encodeURIComponent(q)}&scope=${scope}`);
        const data = await res.json();
        const ms   = (performance.now() - t0).toFixed(0);

        if (!res.ok) {
            status.textContent = '❌ ' + (data.error || res.statusText);
            return;
        }

        status.innerHTML = `✅ <strong>${data.total}</strong> hits &nbsp;·&nbsp;
            Auto-detected: <span class="qtype-pill">${data.query_type}</span> &nbsp;·&nbsp; ${ms} ms`;

        results.innerHTML = renderSearchResults(data.results, data.scope, q);
    } catch(e) {
        status.textContent = '❌ ' + e.message;
    }
}

// ── Shared search result renderer ─────────────────────────────────────────────
// #render: handles both RowHit[] (scope=row) and ColumnHit[] (scope=column/database).
function renderSearchResults(hits, scope, q) {
    if (!hits || hits.length === 0)
        return `<p style="color:var(--text-muted);text-align:center;padding:2rem 0;">No results found for "<strong>${esc(q)}</strong>"</p>`;

    if (scope === 'row') return renderRowHits(hits, q);
    return renderColumnHits(hits, q);
}

function renderRowHits(hits, q) {
    if (!hits || hits.length === 0) return '<p style="color:var(--text-muted)">No row hits.</p>';

    // Collect all unique column names
    const colSet = new Set();
    hits.forEach(h => { if (h.row) Object.keys(h.row).forEach(c => colSet.add(c)); });
    const cols = [...colSet];

    let html = `<div style="overflow-x:auto"><table class="intel-table">
        <thead><tr>
            <th>PK</th>${cols.map(c => `<th>${esc(c)}</th>`).join('')}<th>Matched</th>
        </tr></thead><tbody>`;

    hits.forEach(h => {
        html += `<tr>
            <td><a href="#" onclick="openEntityById('${esc(h.pk_value)}');return false"
                style="color:var(--primary)">${esc(h.pk_value)}</a></td>`;
        cols.forEach(c => {
            const v = h.row ? h.row[c] : null;
            const display = (v === null || v === undefined) ? '<em style="opacity:.4">—</em>'
                : highlightTerm(typeof v==='object'?JSON.stringify(v):v, q);
            html += `<td>${display}</td>`;
        });
        const matched = (h.matched_columns || []).map(m => `<span class="hit-badge">${esc(m)}</span>`).join('');
        html += `<td>${matched || '—'}</td></tr>`;
    });

    html += '</tbody></table></div>';
    return html;
}

function renderColumnHits(hits, q) {
    if (!hits || hits.length === 0) return '<p style="color:var(--text-muted)">No column hits.</p>';

    let html = `<div style="overflow-x:auto"><table class="intel-table">
        <thead><tr>
            <th>Source</th><th>Table</th><th>Column</th>
            <th>PK → Value</th><th>Sample Value</th>
        </tr></thead><tbody>`;

    hits.forEach(h => {
        html += `<tr>
            <td>${esc(h.data_source_id)}</td>
            <td>${esc(h.schema)}.${esc(h.table)}</td>
            <td><span class="hit-badge">${esc(h.column)}</span></td>
            <td><code>${esc(h.pk_column)}</code> = <code>${esc(h.pk_value)}</code></td>
            <td>${highlightTerm(h.sample_value, q)}</td>
        </tr>`;
    });

    html += '</tbody></table></div>';
    return html;
}

// ── Cases ─────────────────────────────────────────────────────────────────────
// #cases: loads GET /api/cases with optional filters.
async function loadCases() {
    const q      = (document.getElementById('case-q')     || {}).value || '';
    const status = (document.getElementById('case-status')|| {}).value || '';
    const el     = document.getElementById('cases-list');
    if (!el) return;
    el.innerHTML = '<p style="color:var(--text-muted)">Loading …</p>';

    const params = new URLSearchParams({ limit: 50 });
    if (q)      params.set('q', q);
    if (status) params.set('status', status);

    try {
        const res  = await fetch('/api/cases?' + params);
        const data = await res.json();
        if (!res.ok) { el.innerHTML = `<p style="color:red">Error: ${esc(data.error)}</p>`; return; }

        if (!data.cases || data.cases.length === 0) {
            el.innerHTML = '<p style="color:var(--text-muted);text-align:center;padding:2rem">No cases found.</p>';
            return;
        }

        el.innerHTML = data.cases.map(c => `
            <div class="case-card">
                <div style="flex:1;min-width:0">
                    <div style="display:flex;align-items:center;gap:.5rem;flex-wrap:wrap;margin-bottom:.25rem">
                        <strong>${esc(c.case_number)}</strong>
                        ${statusBadge(c.status)}
                        ${c.priority && c.priority !== 'normal' ? `<span class="status-badge" style="background:rgba(239,68,68,.15);color:#ef4444">${esc(c.priority)}</span>` : ''}
                    </div>
                    <div style="font-weight:600;margin-bottom:.2rem">${esc(c.title)}</div>
                    <div style="font-size:.82rem;color:var(--text-muted)">
                        ${c.category ? esc(c.category) + ' &nbsp;·&nbsp; ' : ''}
                        ${c.investigating_officer ? 'IO: ' + esc(c.investigating_officer) + ' &nbsp;·&nbsp; ' : ''}
                        ${fmtDate(c.created_at)}
                    </div>
                </div>
                <div style="display:flex;flex-direction:column;gap:.35rem;flex-shrink:0">
                    <button class="btn btn-secondary btn-sm" onclick="openCase('${esc(c.id)}')">View</button>
                </div>
            </div>`).join('');
    } catch(e) {
        el.innerHTML = `<p style="color:red">${esc(e.message)}</p>`;
    }
}

// Open a case detail (loads linked entities).
// #case-detail: GET /api/cases/{id}
async function openCase(id) {
    try {
        const res  = await fetch('/api/cases/' + encodeURIComponent(id));
        const data = await res.json();
        if (!res.ok) { alert(data.error || 'Failed'); return; }
        const c = data.case;
        const entities = data.entities || [];

        let body = `
            <div class="bio-grid" style="margin-bottom:1.5rem">
                ${bioField('Case Number', c.case_number)}
                ${bioField('Status', c.status)}
                ${bioField('Category', c.category)}
                ${bioField('FIR Number', c.fir_number)}
                ${bioField('Jurisdiction', c.jurisdiction)}
                ${bioField('IO', c.investigating_officer)}
                ${bioField('Priority', c.priority)}
                ${bioField('Created', fmtDate(c.created_at))}
            </div>
            <h4 style="font-weight:700;margin-bottom:.75rem">Description</h4>
            <p style="color:var(--text-secondary);font-size:.9rem;margin-bottom:1.5rem">${esc(c.description || '—')}</p>
            <h4 style="font-weight:700;margin-bottom:.75rem">Linked Entities (${entities.length})</h4>`;

        body += entities.map(e => `
            <div style="display:flex;align-items:center;gap:.75rem;padding:.5rem 0;border-bottom:1px solid var(--border)">
                <span class="hit-badge" style="background:rgba(245,158,11,.15);color:#f59e0b">${esc(e.role)}</span>
                <div style="flex:1">
                    <a href="#" onclick="openEntityById('${esc(e.entity_id)}');return false"
                        style="font-weight:600;color:var(--primary)">${esc(e.full_name)}</a>
                    <div style="font-size:.8rem;color:var(--text-muted)">${esc(e.primary_phone)} ${e.primary_email ? '· ' + esc(e.primary_email) : ''}</div>
                </div>
            </div>`).join('');

        openEntityModal('Case: ' + c.case_number, body);
    } catch(e) {
        alert(e.message);
    }
}

// ── Entity Profile ────────────────────────────────────────────────────────────
// #entity-profile: GET /api/entities/{id}/profile
async function openEntityById(id) {
    if (!id) return;
    openEntityModal('Loading …', '<p style="color:var(--text-muted)">Fetching profile …</p>');
    try {
        const res  = await fetch('/api/entities/' + encodeURIComponent(id) + '/profile');
        const data = await res.json();
        if (!res.ok) { document.getElementById('entity-modal-body').innerHTML = `<p style="color:red">${esc(data.error)}</p>`; return; }

        const e = data.entity;
        document.getElementById('entity-modal-name').textContent = e.full_name + (e.display_name ? ' (' + e.display_name + ')' : '');
        document.getElementById('entity-modal-body').innerHTML   = renderEntityProfile(data);
        window._currentEntityId = id;
    } catch(ex) {
        document.getElementById('entity-modal-body').innerHTML = `<p style="color:red">${esc(ex.message)}</p>`;
    }
}

function renderEntityProfile(p) {
    const e = p.entity;
    let html = '';

    // Bio
    html += `<div class="profile-section"><h4>📋 Bio Data</h4><div class="bio-grid">
        ${bioField('Entity Type', e.entity_type)}
        ${bioField('Gender', e.gender)}
        ${bioField('DOB', e.date_of_birth ? e.date_of_birth.slice(0,10) : '')}
        ${bioField('Nationality', e.nationality)}
        ${bioField('Religion', e.religion)}
        ${bioField('Occupation', e.occupation)}
        ${bioField('Phone', e.primary_phone)}
        ${bioField('Email', e.primary_email)}
    </div></div>`;

    // Photo
    if (e.photo_url) html += `<div class="profile-section"><img src="${esc(e.photo_url)}" style="max-height:160px;border-radius:8px;"></div>`;

    // Aliases
    if (e.alias && e.alias.length) html += `<div class="profile-section"><h4>🏷️ Aliases</h4><p>${e.alias.map(a => `<span class="hit-badge">${esc(a)}</span>`).join(' ')}</p></div>`;

    // Addresses
    if (p.addresses && p.addresses.length) {
        html += `<div class="profile-section"><h4>📍 Addresses</h4>`;
        p.addresses.forEach(a => {
            html += `<div style="background:var(--accent-light);border-radius:var(--radius-md);padding:.75rem;margin-bottom:.5rem">
                <strong>${esc(a.address_type)}</strong>${a.is_primary?' <span class="hit-badge">Primary</span>':''}${a.is_verified?' ✅':''}
                <div style="font-size:.88rem;margin-top:.25rem;color:var(--text-secondary)">
                    ${[a.address_line1,a.address_line2,a.city,a.district,a.state,a.pincode,a.country].filter(Boolean).map(esc).join(', ')}
                </div>
                ${a.valid_to ? `<div style="font-size:.75rem;color:var(--text-muted)">Valid until: ${fmtDate(a.valid_to)}</div>` : ''}
            </div>`;
        });
        html += '</div>';
    }

    // Documents
    if (p.documents && p.documents.length) {
        html += `<div class="profile-section"><h4>🪪 Identity Documents</h4><table class="intel-table">
            <thead><tr><th>Type</th><th>Number</th><th>Issued By</th><th>Verified</th></tr></thead><tbody>`;
        p.documents.forEach(d => {
            html += `<tr><td>${esc(d.doc_type)}</td><td><code>${esc(d.doc_number)}</code></td>
                <td>${esc(d.issued_by)}</td><td>${d.is_verified?'✅':'—'}</td></tr>`;
        });
        html += '</tbody></table></div>';
    }

    // Contacts
    if (p.contacts && p.contacts.length) {
        html += `<div class="profile-section"><h4>📞 Contacts</h4><table class="intel-table">
            <thead><tr><th>Type</th><th>Value</th><th>Label</th><th>Primary</th></tr></thead><tbody>`;
        p.contacts.forEach(c => {
            html += `<tr><td>${esc(c.contact_type)}</td><td>${esc(c.contact_value)}</td>
                <td>${esc(c.label)}</td><td>${c.is_primary?'✅':'—'}</td></tr>`;
        });
        html += '</tbody></table></div>';
    }

    // Social
    if (p.social_accounts && p.social_accounts.length) {
        html += `<div class="profile-section"><h4>📱 Social Accounts</h4><table class="intel-table">
            <thead><tr><th>Platform</th><th>Handle</th><th>Followers</th><th>Active</th></tr></thead><tbody>`;
        p.social_accounts.forEach(s => {
            html += `<tr><td>${esc(s.platform)}</td>
                <td>${s.profile_url ? `<a href="${esc(s.profile_url)}" target="_blank">${esc(s.handle)}</a>` : esc(s.handle)}</td>
                <td>${s.followers_count ? s.followers_count.toLocaleString() : '—'}</td>
                <td>${s.is_active?'✅':'—'}</td></tr>`;
        });
        html += '</tbody></table></div>';
    }

    // Bank accounts
    if (p.bank_accounts && p.bank_accounts.length) {
        html += `<div class="profile-section"><h4>🏦 Bank Accounts</h4><table class="intel-table">
            <thead><tr><th>Account No.</th><th>Bank</th><th>IFSC</th><th>Type</th></tr></thead><tbody>`;
        p.bank_accounts.forEach(b => {
            html += `<tr><td><code>${esc(b.account_number)}</code></td>
                <td>${esc(b.bank_name)}</td><td>${esc(b.ifsc_code)}</td>
                <td>${esc(b.account_type)}</td></tr>`;
        });
        html += '</tbody></table></div>';
    }

    // Cases
    if (p.cases && p.cases.length) {
        html += `<div class="profile-section"><h4>📁 Cases</h4><table class="intel-table">
            <thead><tr><th>Case No.</th><th>Title</th><th>Status</th><th>Role</th><th>Added</th></tr></thead><tbody>`;
        p.cases.forEach(c => {
            html += `<tr>
                <td><a href="#" onclick="openCase('${esc(c.case_id)}');return false"
                    style="color:var(--primary)">${esc(c.case_number)}</a></td>
                <td>${esc(c.title)}</td><td>${statusBadge(c.status)}</td>
                <td><span class="hit-badge">${esc(c.role)}</span></td>
                <td>${fmtDate(c.added_at)}</td></tr>`;
        });
        html += '</tbody></table></div>';
    }

    return html;
}

function bioField(label, value) {
    return `<div class="bio-item"><div class="bio-label">${esc(label)}</div>
        <div class="bio-value">${esc(value || '—')}</div></div>`;
}

function openEntityModal(title, body) {
    document.getElementById('entity-modal-name').textContent = title;
    document.getElementById('entity-modal-body').innerHTML   = body;
    document.getElementById('entity-modal').style.display   = 'flex';
}

function closeEntityModal() {
    document.getElementById('entity-modal').style.display = 'none';
    window._currentEntityId = null;
}

// Export the currently-open entity profile.
// #export: calls GET /api/entities/{id}/export?format=json|csv
function exportEntity(format) {
    const id = window._currentEntityId;
    if (!id) { alert('No entity selected'); return; }
    window.location.href = `/api/entities/${encodeURIComponent(id)}/export?format=${format}`;
}

// ── Work Sessions ─────────────────────────────────────────────────────────────
// #work-sessions: loads GET /api/work-sessions
async function loadWorkSessions() {
    const el = document.getElementById('sessions-list');
    if (!el) return;
    el.innerHTML = '<p style="color:var(--text-muted)">Loading …</p>';

    try {
        const res  = await fetch('/api/work-sessions?limit=30');
        const data = await res.json();
        if (!res.ok) { el.innerHTML = `<p style="color:red">${esc(data.error)}</p>`; return; }

        const sessions = data.sessions || [];
        if (!sessions.length) {
            el.innerHTML = '<p style="color:var(--text-muted);text-align:center;padding:2rem">No sessions yet. Click "+ Start Session" to begin.</p>';
            return;
        }

        el.innerHTML = sessions.map(s => `
            <div class="session-row">
                <div class="session-dot ${s.ended_at ? 'ended' : ''}"></div>
                <div style="flex:1;min-width:0">
                    <div style="font-weight:600;font-size:.9rem">${esc(s.description || 'Work Session')}</div>
                    <div style="font-size:.78rem;color:var(--text-muted)">
                        Started: ${fmtDate(s.started_at)}
                        ${s.ended_at ? ' &nbsp;·&nbsp; Ended: ' + fmtDate(s.ended_at) : ' &nbsp;·&nbsp; <span style="color:#10b981">ACTIVE</span>'}
                    </div>
                </div>
                ${!s.ended_at ? `<button class="btn btn-secondary btn-sm" onclick="endWorkSession('${esc(s.id)}')">End</button>` : ''}
            </div>`).join('');
    } catch(e) {
        el.innerHTML = `<p style="color:red">${esc(e.message)}</p>`;
    }
}

async function startWorkSession() {
    const desc = prompt('Session description (optional):') || '';
    try {
        const res  = await fetch('/api/work-sessions', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ description: desc }),
        });
        const data = await res.json();
        if (!res.ok) { alert(data.error || 'Failed'); return; }
        alert('Session started: ' + data.session_id);
        loadWorkSessions();
    } catch(e) { alert(e.message); }
}

async function endWorkSession(id) {
    try {
        const res  = await fetch('/api/work-sessions/' + encodeURIComponent(id) + '/end', { method: 'PATCH' });
        const data = await res.json();
        if (!res.ok) { alert(data.error || 'Failed'); return; }
        loadWorkSessions();
    } catch(e) { alert(e.message); }
}
