package schema

import (
	"github.com/google/uuid"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// UserModelRestriction is the immutable, revisioned user model policy snapshot.
// Runtime persistence is applied by migration 246. Heads, composite revision
// foreign keys, immutable history and the identity catalog are SQL-owned; Ent
// Schema.Create is not a replacement for ApplyMigrations.
type UserModelRestriction struct{ ent.Schema }

func (UserModelRestriction) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "user_model_restriction_rules"}}
}

func (UserModelRestriction) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").Unique().Immutable(),
		field.Int64("user_id"), field.Int64("revision"),
		field.UUID("rule_id", uuid.UUID{}).Default(uuid.New), field.String("action").MaxLen(24).Default("deny_model"), field.Int("limit_value").Optional().Nillable(), field.Time("starts_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}), field.String("source").MaxLen(16).Default("manual"),
		field.String("namespace").MaxLen(64), field.String("canonical_model").MaxLen(255),
		field.Bool("enabled").Default(true), field.Time("expires_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.String("reason").Default("").SchemaType(map[string]string{dialect.Postgres: "text"}), field.Int64("created_by").Optional().Nillable(),
		field.Time("created_at").Immutable().Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (UserModelRestriction) Edges() []ent.Edge {
	return []ent.Edge{edge.To("user", User.Type).Unique().Required().Field("user_id").Annotations(entsql.OnDelete(entsql.Cascade))}
}

func (UserModelRestriction) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "revision", "namespace", "canonical_model", "action", "source").Unique().StorageKey("uq_user_model_restriction_rules_key")}
}
