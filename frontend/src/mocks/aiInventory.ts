import { withLatency } from './_helpers'
import type { AiInventoryData } from '@/types/aiInventory'

// DEMO data for the AI Ecosystem Inventory page. Values mirror the UI spec;
// never real customer telemetry. Served through withLatency like the other
// mock modules.

const DEMO_INVENTORY: AiInventoryData = {
  total: 376,

  by_category: [
    { id: 'cat-1', label: 'AI Agents', count: 112 },
    { id: 'cat-2', label: 'Large Language Models (LLMs)', count: 63 },
    { id: 'cat-3', label: 'Connected Tools & APIs', count: 52 },
    { id: 'cat-4', label: 'RAG Knowledge Bases', count: 38 },
    { id: 'cat-5', label: 'MCP Servers', count: 38 },
  ],

  by_subcategory: [
    { id: 'sub-1', label: 'Model Context Protocol (MCP)', count: 35 },
    { id: 'sub-2', label: 'Vector Databases', count: 18 },
    { id: 'sub-3', label: 'Orchestration Frameworks', count: 17 },
    { id: 'sub-4', label: 'Agent Toolsets', count: 16 },
    { id: 'sub-5', label: 'Fine-tuning Pipelines', count: 16 },
  ],

  by_type: [
    { id: 'typ-1', label: 'Foundation Model', count: 187, color: 'hsl(262 83% 64%)' },
    { id: 'typ-2', label: 'Autonomous Agent', count: 64, color: 'hsl(161 84% 42%)' },
    { id: 'typ-3', label: 'Integration Tool / Plugin', count: 50, color: 'hsl(43 96% 56%)' },
    { id: 'typ-4', label: 'MCP Server Host', count: 45, color: 'hsl(262 83% 66%)' },
    { id: 'typ-5', label: '+2 More', count: 30, color: 'hsl(215 16% 55%)' },
  ],

  assets: [
    {
      id: 'a1',
      name: 'Customer Support Agent',
      subtitle: 'AI Agents / Autonomous',
      category: 'agent',
      connections: 10,
      type: 'Autonomous Agent',
      usage: '14 departments',
      usage_icon: 'workspace',
      status: 'unreviewed',
    },
    {
      id: 'a2',
      name: 'Salesforce Write API',
      subtitle: 'Connected Tools & APIs',
      category: 'tool',
      connections: 1,
      type: 'Integration Tool / Plugin',
      usage: '7 agents using this',
      usage_icon: 'agent',
      status: 'unreviewed',
    },
    {
      id: 'a3',
      name: 'Enterprise Knowledge Base',
      subtitle: 'RAG Knowledge Bases',
      category: 'rag',
      connections: 4,
      type: 'RAG Store',
      usage: '5 workspaces',
      usage_icon: 'workspace',
      status: 'unreviewed',
    },
    {
      id: 'a4',
      name: 'Local Filesystem MCP',
      subtitle: 'Model Context Protocol (MCP)',
      category: 'mcp',
      connections: 11,
      type: 'MCP Server Host',
      usage: '5 agents using this',
      usage_icon: 'agent',
      status: 'unreviewed',
    },
    {
      id: 'a5',
      name: 'Claude 3.5 Sonnet',
      subtitle: 'Foundation Model / LLM',
      category: 'model',
      connections: 23,
      type: 'Foundation Model',
      usage: '18 systems',
      usage_icon: 'system',
      status: 'reviewed',
    },
    {
      id: 'a6',
      name: 'GPT-4o',
      subtitle: 'Foundation Model / LLM',
      category: 'model',
      connections: 31,
      type: 'Foundation Model',
      usage: '22 systems',
      usage_icon: 'system',
      status: 'unreviewed',
    },
    {
      id: 'a7',
      name: 'Lead Enrichment Agent',
      subtitle: 'AI Agents / Autonomous',
      category: 'agent',
      connections: 6,
      type: 'Autonomous Agent',
      usage: '3 departments',
      usage_icon: 'workspace',
      status: 'flagged',
    },
    {
      id: 'a8',
      name: 'Pinecone Vector Index',
      subtitle: 'Vector Databases',
      category: 'rag',
      connections: 9,
      type: 'RAG Store',
      usage: '6 agents using this',
      usage_icon: 'agent',
      status: 'unreviewed',
    },
    {
      id: 'a9',
      name: 'GitHub MCP Server',
      subtitle: 'Model Context Protocol (MCP)',
      category: 'mcp',
      connections: 7,
      type: 'MCP Server Host',
      usage: '4 agents using this',
      usage_icon: 'agent',
      status: 'unreviewed',
    },
    {
      id: 'a10',
      name: 'Stripe Billing API',
      subtitle: 'Connected Tools & APIs',
      category: 'tool',
      connections: 2,
      type: 'Integration Tool / Plugin',
      usage: '2 systems',
      usage_icon: 'system',
      status: 'reviewed',
    },
  ],
}

export const aiInventoryMock = {
  overview: () => withLatency(DEMO_INVENTORY),
}
