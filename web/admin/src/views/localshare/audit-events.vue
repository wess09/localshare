<script setup lang="ts">
import { onMounted, ref } from "vue";
import { Refresh } from "@element-plus/icons-vue";
import { listAuditEvents, type AuditEvent } from "@/api/localshare";
import { message } from "@/utils/message";

defineOptions({
  name: "LocalshareAuditEvents"
});

const loading = ref(false);
const events = ref<AuditEvent[]>([]);

function formatTime(value?: string) {
  return value ? new Date(value).toLocaleString() : "-";
}

function formatDetail(detail?: Record<string, any>) {
  if (!detail || Object.keys(detail).length === 0) return "-";
  return JSON.stringify(detail);
}

async function load() {
  loading.value = true;
  try {
    const data = await listAuditEvents();
    events.value = data.events ?? [];
  } catch (error: any) {
    message(error?.response?.data?.error ?? "加载审计日志失败", {
      type: "error"
    });
  } finally {
    loading.value = false;
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
        <h2 class="text-xl font-semibold text-slate-900">审计日志</h2>
        <p class="mt-1 text-sm text-slate-500">最近的后台管理动作。</p>
      </div>
      <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
    </div>

    <el-card shadow="never">
      <el-table :data="events" size="small" v-loading="loading">
        <el-table-column label="时间" min-width="170">
          <template #default="{ row }">
            {{ formatTime(row.created_at) }}
          </template>
        </el-table-column>
        <el-table-column prop="actor" label="操作者" width="120" />
        <el-table-column prop="action" label="动作" width="150" />
        <el-table-column prop="target" label="目标" min-width="160" />
        <el-table-column label="详情" min-width="260" show-overflow-tooltip>
          <template #default="{ row }">
            {{ formatDetail(row.detail) }}
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>
