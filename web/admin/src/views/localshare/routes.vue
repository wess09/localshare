<script setup lang="ts">
import { onMounted, ref } from "vue";
import { Refresh } from "@element-plus/icons-vue";
import { ElMessageBox } from "element-plus";
import { deleteRoute, listRoutes, type RouteItem } from "@/api/localshare";
import { message } from "@/utils/message";

defineOptions({
  name: "LocalshareRoutes"
});

const loading = ref(false);
const routes = ref<RouteItem[]>([]);

function formatTime(value?: string) {
  return value ? new Date(value).toLocaleString() : "-";
}

async function load() {
  loading.value = true;
  try {
    const data = await listRoutes();
    routes.value = data.routes ?? [];
  } catch (error: any) {
    message(error?.response?.data?.error ?? "加载路由失败", { type: "error" });
  } finally {
    loading.value = false;
  }
}

async function remove(row: RouteItem) {
  try {
    await ElMessageBox.confirm(`确认删除路由 ${row.token}？`, "删除路由", {
      type: "warning",
      confirmButtonText: "删除",
      cancelButtonText: "取消"
    });
    await deleteRoute(row.token);
    message("路由已删除", { type: "success" });
    await load();
  } catch (error: any) {
    if (error === "cancel" || error === "close") return;
    message(error?.response?.data?.error ?? "删除失败", { type: "error" });
  }
}

onMounted(() => {
  void load();
});
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between gap-3">
      <div>
        <h2 class="text-xl font-semibold text-slate-900">路由管理</h2>
        <p class="mt-1 text-sm text-slate-500">查看当前 token 到节点的路由状态。</p>
      </div>
      <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
    </div>

    <el-card shadow="never">
      <el-table :data="routes" size="small" v-loading="loading">
        <el-table-column prop="token" label="Token" min-width="150" />
        <el-table-column prop="node_id" label="节点" min-width="140" />
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.status === 'active' ? 'success' : 'info'">
              {{ row.status }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="target_url" label="节点目标" min-width="220" show-overflow-tooltip />
        <el-table-column prop="public_url" label="公网入口" min-width="220" show-overflow-tooltip />
        <el-table-column label="过期时间" min-width="170">
          <template #default="{ row }">
            {{ formatTime(row.expires_at) }}
          </template>
        </el-table-column>
        <el-table-column fixed="right" label="操作" width="100">
          <template #default="{ row }">
            <el-button link type="danger" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>
