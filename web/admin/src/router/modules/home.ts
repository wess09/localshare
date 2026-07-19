const Layout = () => import("@/layout/index.vue");

export default {
  path: "/",
  name: "Home",
  component: Layout,
  redirect: "/dashboard",
  meta: {
    icon: "ri/dashboard-3-line",
    title: "Localshare",
    rank: 0
  },
  children: [
    {
      path: "/dashboard",
      name: "Dashboard",
      component: () => import("@/views/localshare/dashboard.vue"),
      meta: {
        title: "运行概览",
        icon: "ri/dashboard-3-line",
        fixedTag: true
      }
    },
    {
      path: "/nodes",
      name: "Nodes",
      component: () => import("@/views/localshare/nodes.vue"),
      meta: {
        title: "节点管理",
        icon: "ri/server-line"
      }
    },
    {
      path: "/routes",
      name: "Routes",
      component: () => import("@/views/localshare/routes.vue"),
      meta: {
        title: "路由管理",
        icon: "ri/route-line"
      }
    },
    {
      path: "/audit-events",
      name: "AuditEvents",
      component: () => import("@/views/localshare/audit-events.vue"),
      meta: {
        title: "审计日志",
        icon: "ri/file-list-3-line"
      }
    },
    {
      path: "/settings",
      name: "ClusterSettings",
      component: () => import("@/views/localshare/settings.vue"),
      meta: {
        title: "集群参数",
        icon: "ri/settings-3-line"
      }
    }
  ]
} satisfies RouteConfigsTable;
