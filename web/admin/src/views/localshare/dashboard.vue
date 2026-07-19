<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from "vue";
import { Refresh } from "@element-plus/icons-vue";
import {
  adminStats,
  type Stats,
  type NodeItem
} from "@/api/localshare";
import { message } from "@/utils/message";

defineOptions({
  name: "Dashboard"
});

const stats = ref<Stats | null>(null);
const nodes = ref<NodeItem[]>([]);
const loading = ref(false);
let socket: WebSocket | null = null;
let socketTimer: number | undefined;

function formatTime(value?: string) {
  return value ? new Date(value).toLocaleString() : "-";
}

async function load() {
  loading.value = true;
  try {
    const data = await adminStats();
    stats.value = data;
    nodes.value = data.cluster.nodes ?? [];
  } catch (error: any) {
    message(error?.response?.data?.error ?? "刷新失败", { type: "error" });
  } finally {
    loading.value = false;
  }
}

function connect() {
  if (socket) {
    socket.close();
  }
  const url =
    `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}` +
    "/api/v1/admin/ws";
  socket = new WebSocket(url);
  socket.onmessage = event => {
    try {
      const payload = JSON.parse(event.data);
      if (payload.type === "stats") {
        stats.value = payload.data;
        nodes.value = payload.data?.cluster?.nodes ?? [];
      }
    } catch {
      // ignore malformed push
    }
  };
  socket.onclose = () => {
    window.clearTimeout(socketTimer);
    socketTimer = window.setTimeout(connect, 3000);
  };
}

onMounted(() => {
  void load();
  connect();
});

onBeforeUnmount(() => {
  window.clearTimeout(socketTimer);
  socket?.close();
});
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between gap-3">
      <div>
        <h2 class="text-xl font-semibold text-slate-900">运行概览</h2>
        <p class="mt-1 text-sm text-slate-500">
          {{ stats ? `节点 ${stats.node_id} · ${stats.role}` : "正在加载" }}
        </p>
      </div>
      <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
    </div>

    <el-row :gutter="12">
      <el-col :xs="24" :sm="12" :lg="6">
        <el-card shadow="never">
          <div class="text-sm text-slate-500">SSH 连接</div>
          <div class="mt-2 text-2xl font-semibold">{{ stats?.ssh.active ?? 0 }}</div>
          <div class="mt-1 text-xs text-slate-400">总计 {{ stats?.ssh.total ?? 0 }}</div>
        </el-card>
      </el-col>
      <el-col :xs="24" :sm="12" :lg="6">
        <el-card shadow="never">
          <div class="text-sm text-slate-500">信令 Peer</div>
          <div class="mt-2 text-2xl font-semibold">{{ stats?.signal.peers ?? 0 }}</div>
          <div class="mt-1 text-xs text-slate-400">
            Viewer {{ stats?.signal.viewers ?? 0 }}
          </div>
        </el-card>
      </el-col>
      <el-col :xs="24" :sm="12" :lg="6">
        <el-card shadow="never">
          <div class="text-sm text-slate-500">节点</div>
          <div class="mt-2 text-2xl font-semibold">{{ nodes.length }}</div>
          <div class="mt-1 text-xs text-slate-400">
            可用 {{ nodes.filter(item => item.eligible).length }}
          </div>
        </el-card>
      </el-col>
      <el-col :xs="24" :sm="12" :lg="6">
        <el-card shadow="never">
          <div class="text-sm text-slate-500">路由</div>
          <div class="mt-2 text-2xl font-semibold">
            {{ stats?.cluster.routes_active ?? 0 }}
          </div>
          <div class="mt-1 text-xs text-slate-400">
            总计 {{ stats?.cluster.routes_total ?? 0 }}
          </div>
        </el-card>
      </el-col>
    </el-row>

    <el-card shadow="never">
      <template #header>节点状态</template>
      <el-table :data="nodes.slice(0, 8)" size="small" stripe>
        <el-table-column prop="node_id" label="节点" min-width="150" />
        <el-table-column label="状态" width="140">
          <template #default="{ row }">
            <el-tag :type="row.healthy ? 'success' : 'danger'">
              {{ row.healthy ? "健康" : "异常" }}
            </el-tag>
            <el-tag class="ml-2" :type="row.eligible ? 'success' : 'warning'">
              {{ row.eligible ? "可调度" : "不可调度" }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="ssh_server" label="SSH" min-width="170" />
        <el-table-column prop="public_base_url" label="公网入口" min-width="170" />
        <el-table-column label="容量" width="180">
          <template #default="{ row }">
            {{ row.current_tunnels }}/{{ row.max_tunnels }}
            · {{ row.active_connections }}/{{ row.max_active_connections || "∞" }}
          </template>
        </el-table-column>
        <el-table-column label="最近心跳" min-width="170">
          <template #default="{ row }">
            {{ formatTime(row.last_heartbeat) }}
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>
