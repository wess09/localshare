package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Node struct {
	ent.Schema
}

func (Node) Fields() []ent.Field {
	return []ent.Field{
		field.String("node_id").Unique().NotEmpty(),
		field.String("ssh_server").Default(""),
		field.String("public_base_url").Default(""),
		field.Int("weight").Default(100).Positive(),
		field.Bool("enabled").Default(true),
		field.Bool("maintenance").Default(false),
		field.Int("max_tunnels").Default(100000).NonNegative(),
		field.Int("current_tunnels").Default(0).NonNegative(),
		field.Int("max_active_connections").Default(0).NonNegative(),
		field.Int("active_connections").Default(0).NonNegative(),
		field.String("region").Default("default"),
		field.String("token").Default(""),
		field.Time("last_heartbeat").Default(time.Now),
		field.Bool("is_local").Default(false),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (Node) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("enabled", "maintenance"),
		index.Fields("last_heartbeat"),
	}
}
