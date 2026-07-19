<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import { Plus, Refresh } from "@element-plus/icons-vue";
import {
  listClusterSettings,
  upsertClusterSetting,
  type ClusterSetting
} from "@/api/localshare";
import { message } from "@/utils/message";

defineOptions({
  name: "LocalshareSettings"
});

type SettingRow = ClusterSetting & { draft: string; saving?: boolean };

const loading = ref(false);
const settings = ref<SettingRow[]>([]);
const createForm = reactive({
  key: "",
  value: ""
});

function formatTime(value?: string) {
  return value ? new Date(value).toLocaleString() : "-";
}

async function load() {
  loading.value = true;
  try {
    const data = await listClusterSettings();
    settings.value = (data.settings ?? []).map(item => ({
      ...item,
      draft: item.value
    }));
  } catch (error: any) {
    message(error?.response?.data?.error ?? "加载集群参数失败", {
      type: "error"
    });
  } finally {
    loading.value = false;
  }
}

async function save(row: SettingRow) {
  row.saving = true;
  try {
    await upsertClusterSetting(row.key, row.draft);
    message("参数已保存", { type: "success" });
    await load();
  } catch (error: any) {
    message(error?.response?.data?.error ?? "保存失败", { type: "error" });
  } finally {
    row.saving = false;
  }
}

async function createSetting() {
  if (!createForm.key.trim()) {
    message("请输入参数名", { type: "warning" });
    return;
  }
  try {
    await upsertClusterSetting(createForm.key.trim(), createForm.value);
    createForm.key = "";
    createForm.value = "";
    message("参数已创建", { type: "success" });
    await load();
  } catch (error: any) {
    message(error?.response?.data?.error ?? "创建失败", { type: "error" });
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
        <h2 class="text-xl font-semibold text-slate-900">集群参数</h2>
        <p class="mt-1 text-sm text-slate-500">维护存储在 PostgreSQL 的集群配置。</p>
      </div>
      <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
    </div>

    <el-card shadow="never">
      <el-form :inline="true" :model="createForm" class="mb-3">
        <el-form-item label="参数名">
          <el-input v-model="createForm.key" placeholder="例如 scheduler.mode" />
        </el-form-item>
        <el-form-item label="值">
          <el-input v-model="createForm.value" placeholder="参数值" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :icon="Plus" @click="createSetting">
            新增
          </el-button>
        </el-form-item>
      </el-form>

      <el-table :data="settings" size="small" v-loading="loading">
        <el-table-column prop="key" label="参数" min-width="180" />
        <el-table-column label="值" min-width="260">
          <template #default="{ row }">
            <el-input v-model="row.draft" />
          </template>
        </el-table-column>
        <el-table-column label="更新时间" min-width="170">
          <template #default="{ row }">
            {{ formatTime(row.updated_at) }}
          </template>
        </el-table-column>
        <el-table-column fixed="right" label="操作" width="100">
          <template #default="{ row }">
            <el-button link type="primary" :loading="row.saving" @click="save(row)">
              保存
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>
