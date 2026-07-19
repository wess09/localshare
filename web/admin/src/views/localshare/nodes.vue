<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import { Delete, Refresh } from "@element-plus/icons-vue";
import { ElMessageBox } from "element-plus";
import {
  deleteNode,
  listNodes,
  patchNode,
  patchNodeCapacity,
  setNodeMaintenance,
  setNodeWeight,
  type NodeItem
} from "@/api/localshare";
import { message } from "@/utils/message";

defineOptions({
  name: "LocalshareNodes"
});

const loading = ref(false);
const saving = ref(false);
const nodes = ref<NodeItem[]>([]);
const dialogVisible = ref(false);
const formRef = ref();
const form = reactive({
  node_id: "",
  enabled: true,
  maintenance: false,
  weight: 100,
  max_tunnels: 0,
  max_active_connections: 0,
  region: ""
});

const rules = {
  node_id: [{ required: true, message: "节点 ID 不能为空", trigger: "blur" }],
  weight: [{ required: true, message: "请输入权重", trigger: "blur" }]
};

function formatTime(value?: string) {
  return value ? new Date(value).toLocaleString() : "-";
}

async function load() {
  loading.value = true;
  try {
    const data = await listNodes();
    nodes.value = data.nodes ?? [];
  } catch (error: any) {
    message(error?.response?.data?.error ?? "加载节点失败", { type: "error" });
  } finally {
    loading.value = false;
  }
}

function openEditor(row: NodeItem) {
  Object.assign(form, {
    node_id: row.node_id,
    enabled: row.enabled,
    maintenance: row.maintenance,
    weight: row.weight,
    max_tunnels: row.max_tunnels,
    max_active_connections: row.max_active_connections,
    region: row.region
  });
  dialogVisible.value = true;
}

async function save() {
  await formRef.value?.validate(async (valid: boolean) => {
    if (!valid) return;
    saving.value = true;
    try {
      await patchNode(form.node_id, {
        enabled: form.enabled,
        region: form.region
      });
      await setNodeMaintenance(form.node_id, form.maintenance);
      await setNodeWeight(form.node_id, form.weight);
      await patchNodeCapacity(form.node_id, {
        max_tunnels: form.max_tunnels,
        max_active_connections: form.max_active_connections
      });
      message("节点已更新", { type: "success" });
      dialogVisible.value = false;
      await load();
    } catch (error: any) {
      message(error?.response?.data?.error ?? "保存失败", { type: "error" });
    } finally {
      saving.value = false;
    }
  });
}

async function toggleMaintenance(row: NodeItem, value: boolean) {
  try {
    await setNodeMaintenance(row.node_id, value);
    row.maintenance = value;
  } catch (error: any) {
    message(error?.response?.data?.error ?? "更新失败", { type: "error" });
  }
}

async function toggleEnabled(row: NodeItem, value: boolean) {
  try {
    await patchNode(row.node_id, { enabled: value });
    row.enabled = value;
  } catch (error: any) {
    message(error?.response?.data?.error ?? "更新失败", { type: "error" });
  }
}

async function removeNode(row: NodeItem) {
  try {
    await ElMessageBox.confirm(`删除节点 ${row.node_id}？`, "确认删除", {
      type: "warning"
    });
    await deleteNode(row.node_id);
    message("节点已删除", { type: "success" });
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
        <h2 class="text-xl font-semibold text-slate-900">节点管理</h2>
        <p class="mt-1 text-sm text-slate-500">调整节点启用状态、维护模式和容量。</p>
      </div>
      <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
    </div>

    <el-card shadow="never">
      <el-table :data="nodes" size="small" v-loading="loading">
        <el-table-column prop="node_id" label="节点" min-width="150" />
        <el-table-column label="健康" width="100">
          <template #default="{ row }">
            <el-tag :type="row.healthy ? 'success' : 'danger'">
              {{ row.healthy ? "健康" : "异常" }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="启用" width="100">
          <template #default="{ row }">
            <el-switch
              :model-value="row.enabled"
              @change="value => toggleEnabled(row, value as boolean)"
            />
          </template>
        </el-table-column>
        <el-table-column label="维护" width="100">
          <template #default="{ row }">
            <el-switch
              :model-value="row.maintenance"
              @change="value => toggleMaintenance(row, value as boolean)"
            />
          </template>
        </el-table-column>
        <el-table-column prop="ssh_server" label="SSH" min-width="170" />
        <el-table-column prop="public_base_url" label="公网入口" min-width="170" />
        <el-table-column label="容量" width="170">
          <template #default="{ row }">
            {{ row.current_tunnels }}/{{ row.max_tunnels }}
          </template>
        </el-table-column>
        <el-table-column label="权重" width="100" prop="weight" />
        <el-table-column label="最近心跳" min-width="170">
          <template #default="{ row }">
            {{ formatTime(row.last_heartbeat) }}
          </template>
        </el-table-column>
        <el-table-column fixed="right" label="操作" width="160">
          <template #default="{ row }">
            <el-button link type="primary" @click="openEditor(row)">编辑</el-button>
            <el-button link type="danger" :icon="Delete" @click="removeNode(row)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="dialogVisible" title="编辑节点" width="520px">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="140px">
        <el-form-item label="节点 ID" prop="node_id">
          <el-input v-model="form.node_id" disabled />
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="form.enabled" />
        </el-form-item>
        <el-form-item label="维护模式">
          <el-switch v-model="form.maintenance" />
        </el-form-item>
        <el-form-item label="权重" prop="weight">
          <el-input-number v-model="form.weight" :min="1" :max="10000" />
        </el-form-item>
        <el-form-item label="最大隧道数">
          <el-input-number v-model="form.max_tunnels" :min="0" :max="100000" />
        </el-form-item>
        <el-form-item label="最大并发连接">
          <el-input-number
            v-model="form.max_active_connections"
            :min="0"
            :max="100000"
          />
        </el-form-item>
        <el-form-item label="区域">
          <el-input v-model="form.region" placeholder="default" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>
