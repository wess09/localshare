package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type AdminSession struct {
	ent.Schema
}

func (AdminSession) Fields() []ent.Field {
	return []ent.Field{
		field.String("session_id").Unique().NotEmpty(),
		field.String("username").NotEmpty(),
		field.Time("expires_at"),
		field.Time("last_seen").Default(time.Now),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

func (AdminSession) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("username"),
		index.Fields("expires_at"),
	}
}
