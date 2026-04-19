const statusEl = document.getElementById('status');
const gridEl = document.getElementById('qa-grid');
const reloadBtn = document.getElementById('reload-btn');

function escapeHTML(value) {
    return String(value)
        .replaceAll('&', '&amp;')
        .replaceAll('<', '&lt;')
        .replaceAll('>', '&gt;')
        .replaceAll('"', '&quot;')
        .replaceAll("'", '&#39;');
}

function render(items) {
    if (!Array.isArray(items) || items.length === 0) {
        gridEl.innerHTML = '';
        statusEl.textContent = 'No content found.';
        return;
    }

    gridEl.innerHTML = items.map((item) => `
        <article class="card">
            <div class="qid">${escapeHTML(item.id || 'UNLABELED')}</div>
            <div class="q">${escapeHTML(item.question || '')}</div>
            <div class="a">${escapeHTML(item.answer || '')}</div>
        </article>
    `).join('');

    statusEl.textContent = `Loaded ${items.length} items dynamically.`;
}

async function loadData() {
    statusEl.textContent = 'Loading content...';

    try {
        const res = await fetch(`/labs/hadoop-review/static/data.json?t=${Date.now()}`, { cache: 'no-store' });
        if (!res.ok) {
            throw new Error(`HTTP ${res.status}`);
        }

        const payload = await res.json();
        render(payload.items || []);
    } catch (err) {
        gridEl.innerHTML = '';
        statusEl.textContent = `Failed to load content: ${err.message}`;
    }
}

reloadBtn?.addEventListener('click', loadData);
loadData();
