package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type AuditEvent struct {
	ent.Schema
}

func (AuditEvent) Fields() []ent.Field {
	return []ent.Field{
		field.String("actor").Default("system"),
		field.String("action").NotEmpty(),
		field.String("target").Default(""),
		field.JSON("detail", map[string]any{}).Optional(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

func (AuditEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("created_at"),
		index.Fields("action"),
	}
}
