-- =============================================================================
-- ACID Categories System - Database Schema
-- =============================================================================
-- This creates the category system that can be used for:
-- - Employee positions (Developer, Manager, etc.)
-- - Any entity classification
-- - Custom tags
--
-- Categories are GENRIC - can be applied to ANY table/entity type
-- =============================================================================

-- Table to store all categories
CREATE TABLE IF NOT EXISTS categories (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL UNIQUE,
    description TEXT,
    color VARCHAR(20) DEFAULT '#3b82f6', -- Default blue color
    entity_type VARCHAR(50) NOT NULL DEFAULT 'employee', -- What type of entity this belongs to
    icon VARCHAR(50), -- Optional icon (emoji or class name)
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    created_by INTEGER REFERENCES users(id),
    is_active BOOLEAN DEFAULT true
);

-- Index for faster lookups
CREATE INDEX idx_categories_entity_type ON categories(entity_type);
CREATE INDEX idx_categories_is_active ON categories(is_active);

-- Junction table: entities <-> categories (many-to-many relationship)
-- This allows ANY entity to have multiple categories
CREATE TABLE IF NOT EXISTS entity_categories (
    id SERIAL PRIMARY KEY,
    entity_type VARCHAR(50) NOT NULL, -- e.g., 'employee', 'user', 'table', etc.
    entity_id INTEGER NOT NULL, -- The ID of the entity
    category_id INTEGER NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    assigned_at TIMESTAMP DEFAULT NOW(),
    assigned_by INTEGER REFERENCES users(id),
    UNIQUE(entity_type, entity_id, category_id)
);

-- Index for faster lookups
CREATE INDEX idx_entity_categories_entity ON entity_categories(entity_type, entity_id);
CREATE INDEX idx_entity_categories_category ON entity_categories(category_id);

-- Add sample categories for employees
INSERT INTO categories (name, description, color, entity_type, icon) VALUES
    ('Full Stack Developer', 'Works on both frontend and backend', '#8b5cf6', 'employee', '💻'),
    ('Frontend Developer', 'Specializes in UI/UX and frontend frameworks', '#3b82f6', 'employee', '🎨'),
    ('Backend Developer', 'Server-side development and APIs', '#10b981', 'employee', '⚙️'),
    ('DevOps Engineer', 'Infrastructure and deployment automation', '#f59e0b', 'employee', '🚀'),
    ('Data Engineer', 'Data pipelines and processing', '#ec4899', 'employee', '📊'),
    ('Machine Learning Engineer', 'AI/ML model development', '#8b5cf6', 'employee', '🤖'),
    ('Project Manager', 'Manages project timelines and teams', '#14b8a6', 'employee', '📋'),
    ('Tech Lead', 'Technical leadership and mentoring', '#f97316', 'employee', '👔'),
    ('QA Engineer', 'Quality assurance and testing', '#22c55e', 'employee', '✅'),
    ('Database Administrator', 'Database management and optimization', '#0ea5e9', 'employee', '🗄️')
ON CONFLICT (name) DO NOTHING;

-- View to easily get all categories for an entity
CREATE OR REPLACE VIEW v_entity_with_categories AS
SELECT 
    ec.entity_type,
    ec.entity_id,
    array_agg(c.name ORDER BY c.name) FILTER (WHERE c.is_active) as category_names,
    array_agg(c.id ORDER BY c.name) FILTER (WHERE c.is_active) as category_ids,
    json_agg(json_build_object('id', c.id, 'name', c.name, 'color', c.color, 'icon', c.icon) 
        ORDER BY c.name) FILTER (WHERE c.is_active) as categories
FROM entity_categories ec
JOIN categories c ON c.id = ec.category_id AND c.is_active = true
GROUP BY ec.entity_type, ec.entity_id;

-- Function to assign category to entity
CREATE OR REPLACE FUNCTION assign_category(
    p_entity_type VARCHAR,
    p_entity_id INTEGER,
    p_category_id INTEGER,
    p_assigned_by INTEGER DEFAULT NULL
) RETURNS BOOLEAN AS $$
BEGIN
    INSERT INTO entity_categories (entity_type, entity_id, category_id, assigned_by)
    VALUES (p_entity_type, p_entity_id, p_category_id, p_assigned_by)
    ON CONFLICT (entity_type, entity_id, category_id) DO NOTHING;
    RETURN TRUE;
END;
$$ LANGUAGE plpgsql;

-- Function to remove category from entity
CREATE OR REPLACE FUNCTION remove_category(
    p_entity_type VARCHAR,
    p_entity_id INTEGER,
    p_category_id INTEGER
) RETURNS BOOLEAN AS $$
BEGIN
    DELETE FROM entity_categories 
    WHERE entity_type = p_entity_type 
    AND entity_id = p_entity_id 
    AND category_id = p_category_id;
    RETURN TRUE;
END;
$$ LANGUAGE plpgsql;

-- Function to get all entities with a specific category
CREATE OR REPLACE FUNCTION get_entities_by_category(
    p_entity_type VARCHAR,
    p_category_id INTEGER
) RETURNS TABLE(entity_id INTEGER, entity_type VARCHAR, assigned_at TIMESTAMP) AS $$
BEGIN
    RETURN QUERY
    SELECT ec.entity_id, ec.entity_type, ec.assigned_at
    FROM entity_categories ec
    WHERE ec.entity_type = p_entity_type AND ec.category_id = p_category_id;
END;
$$ LANGUAGE plpgsql;

-- Grant permissions
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA PUBLIC TO acid;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA PUBLIC TO acid;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA PUBLIC TO acid;