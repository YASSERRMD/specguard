// eslint-disable-next-line no-unused-vars
import React, { useState, useEffect, useCallback } from 'react';
import { 
  Database, 
  Cpu, 
  ShieldCheck, 
  History, 
  Server as ServerIcon, 
  Activity, 
  Upload, 
  Plus, 
  ChevronDown, 
  ChevronRight, 
  Play, 
  Square, 
  RefreshCw, 
  CheckCircle2, 
  AlertCircle, 
  AlertTriangle,
  FileCode,
  Sliders
} from 'lucide-react';
const DEFAULT_SERVER_URL = typeof window !== 'undefined'
  ? (window.location.port === '5173' || window.location.port === '3000'
      ? 'http://localhost:8080'
      : window.location.origin)
  : 'http://localhost:8080';

// Recursive Schema Tree Viewer Component
function SchemaViewer({ name, schema, depth = 0 }) {
  const [expanded, setExpanded] = useState(depth < 2);
  
  if (!schema) return null;
  
  const isObject = schema.type === 'object';
  const isArray = schema.type === 'array';
  const hasChildren = isObject || isArray;

  const toggle = (e) => {
    e.stopPropagation();
    setExpanded(!expanded);
  };

  const renderTypeDetails = () => {
    if (schema.type === 'scalar') {
      let desc = schema.scalar_type;
      if (schema.constraints && schema.constraints.length > 0) {
        desc += ` (${schema.constraints.map(c => `${c.kind}: ${c.value}`).join(', ')})`;
      }
      return <span style={{ color: '#60a5fa' }}>{desc}</span>;
    }
    if (isArray) {
      return <span style={{ color: '#c084fc' }}>array</span>;
    }
    if (isObject) {
      return <span style={{ color: '#34d399' }}>object</span>;
    }
    return <span style={{ color: '#9ca3af' }}>{schema.type}</span>;
  };

  return (
    <div style={{ marginLeft: `${depth > 0 ? 16 : 0}px`, marginTop: '6px', fontFamily: 'var(--font-mono)' }}>
      <div 
        onClick={hasChildren ? toggle : undefined}
        style={{ 
          display: 'flex', 
          alignItems: 'center', 
          cursor: hasChildren ? 'pointer' : 'default',
          userSelect: 'none',
          padding: '4px 8px',
          borderRadius: '6px',
          backgroundColor: 'rgba(255, 255, 255, 0.015)'
        }}
      >
        {hasChildren ? (
          expanded ? <ChevronDown size={14} style={{ marginRight: '6px', color: 'var(--text-muted)' }} /> : 
                     <ChevronRight size={14} style={{ marginRight: '6px', color: 'var(--text-muted)' }} />
        ) : (
          <div style={{ width: '20px' }} />
        )}
        <span style={{ fontWeight: 600, marginRight: '8px', color: 'var(--text-primary)' }}>{name || 'root'}:</span>
        {renderTypeDetails()}
      </div>
      {expanded && isObject && schema.properties && (
        <div style={{ borderLeft: '1px solid rgba(255,255,255,0.06)', marginLeft: '14px', paddingLeft: '4px' }}>
          {Object.entries(schema.properties).map(([propName, propSchema]) => (
            <SchemaViewer key={propName} name={propName} schema={propSchema} depth={depth + 1} />
          ))}
        </div>
      )}
      {expanded && isArray && schema.item && (
        <div style={{ borderLeft: '1px solid rgba(255,255,255,0.06)', marginLeft: '14px', paddingLeft: '4px' }}>
          <SchemaViewer name="items" schema={schema.item} depth={depth + 1} />
        </div>
      )}
    </div>
  );
}

// Single Operation Accordion Component
function OperationAccordion({ opId, op }) {
  const [open, setOpen] = useState(false);

  const getMethodBadgeClass = (method) => {
    const m = (method || '').toLowerCase();
    if (m === 'get') return 'badge-method badge-get';
    if (m === 'post') return 'badge-method badge-post';
    if (m === 'put') return 'badge-method badge-put';
    if (m === 'delete') return 'badge-method badge-delete';
    return 'badge-method';
  };

  return (
    <div className="op-row">
      <div className="op-header" onClick={() => setOpen(!open)}>
        <span className={getMethodBadgeClass(op.metadata?.method)}>{op.metadata?.method || 'ANY'}</span>
        <span className="op-path">{op.metadata?.path || '/'}</span>
        <span className="op-id">{opId}</span>
        {open ? <ChevronDown size={18} style={{ color: 'var(--text-secondary)' }} /> : 
                <ChevronRight size={18} style={{ color: 'var(--text-secondary)' }} />}
      </div>
      {open && (
        <div className="op-details">
          {op.input && Object.keys(op.input.properties || {}).length > 0 && (
            <div>
              <div className="schema-header" style={{ color: 'var(--accent-primary)' }}>Input schemas (Parameters / Body)</div>
              <div className="schema-block">
                {Object.entries(op.input.properties).map(([key, schema]) => (
                  <SchemaViewer key={key} name={key} schema={schema} />
                ))}
              </div>
            </div>
          )}

          {op.output && Object.keys(op.output.properties || {}).length > 0 && (
            <div>
              <div className="schema-header" style={{ color: 'var(--status-success)' }}>Successful responses</div>
              <div className="schema-block">
                {Object.entries(op.output.properties).map(([status, schema]) => (
                  <SchemaViewer key={status} name={`status: ${status}`} schema={schema} />
                ))}
              </div>
            </div>
          )}

          {op.error_shapes && Object.keys(op.error_shapes || {}).length > 0 && (
            <div>
              <div className="schema-header" style={{ color: 'var(--status-warning)' }}>Error responses</div>
              <div className="schema-block">
                {Object.entries(op.error_shapes).map(([status, schema]) => (
                  <SchemaViewer key={status} name={`status: ${status}`} schema={schema} />
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

export default function App() {
  const [serverUrl, setServerUrl] = useState(DEFAULT_SERVER_URL);
  const [apiKey, setApiKey] = useState(() => {
    return localStorage.getItem('specguard_api_key') || '';
  });
  const [connected, setConnected] = useState(false);
  const [activeTab, setActiveTab] = useState('specs');
  
  // Keep API Key updated in localStorage
  useEffect(() => {
    localStorage.setItem('specguard_api_key', apiKey);
  }, [apiKey]);

  // Helper for authenticated API calls
  const apiFetch = useCallback(async (urlPath, options = {}) => {
    const headers = {
      ...options.headers,
    };
    if (apiKey) {
      headers['Authorization'] = `Bearer ${apiKey}`;
    }
    return fetch(`${serverUrl}${urlPath}`, {
      ...options,
      headers,
    });
  }, [serverUrl, apiKey]);

  // Specs state
  const [specs, setSpecs] = useState([]);
  const [selectedSpecId, setSelectedSpecId] = useState('');
  const [selectedSpec, setSelectedSpec] = useState(null);
  const [newSpecId, setNewSpecId] = useState('');
  const [newSpecRaw, setNewSpecRaw] = useState('');
  const [uploading, setUploading] = useState(false);

  // Mocks state
  const [runningMocks, setRunningMocks] = useState({});
  const [mockConfigs, setMockConfigs] = useState({});
  const [savingConfigSpecId, setSavingConfigSpecId] = useState(null);

  // Contracts state
  const [targetUrl, setTargetUrl] = useState('');
  const [runningCheck, setRunningCheck] = useState(false);
  const [checkResult, setCheckResult] = useState(null);

  // History state
  const [runHistory, setRunHistory] = useState([]);
  const [selectedHistoryRun, setSelectedHistoryRun] = useState(null);

  const loadSpecs = useCallback(async () => {
    try {
      const res = await apiFetch('/api/specs');
      if (res.ok) {
        const data = await res.json();
        setSpecs(data);
        if (data.length > 0 && !selectedSpecId) {
          setSelectedSpecId(data[0]);
        }
      }
    } catch (e) {
      console.error("Failed to load specs:", e);
    }
  }, [apiFetch, selectedSpecId]);

  const loadRunningMocks = useCallback(async () => {
    try {
      const res = await apiFetch('/api/mocks');
      if (res.ok) {
        const data = await res.json();
        setRunningMocks(data);
      }
    } catch (e) {
      console.error("Failed to load running mocks:", e);
    }
  }, [apiFetch]);

  const loadSpecDetails = useCallback(async (id) => {
    try {
      const res = await apiFetch(`/api/specs?id=${encodeURIComponent(id)}`);
      if (res.ok) {
        const data = await res.json();
        setSelectedSpec(data);
      }
    } catch (e) {
      console.error("Failed to load spec details:", e);
    }
  }, [apiFetch]);

  const loadMockConfig = useCallback(async (id) => {
    try {
      const res = await apiFetch(`/api/mocks/config?id=${encodeURIComponent(id)}`);
      if (res.ok) {
        const data = await res.json();
        // Ensure nesting safety
        if (!data.chaos) {
          data.chaos = {
            latency_ms: 0,
            latency_jitter_ms: 0,
            error_rate: 0,
            error_status: 500,
            drop_connection_rate: 0
          };
        }
        setMockConfigs(prev => ({ ...prev, [id]: data }));
      }
    } catch (e) {
      console.error("Failed to load mock config:", e);
    }
  }, [apiFetch]);

  const loadContractHistory = useCallback(async (id) => {
    try {
      const res = await apiFetch(`/api/reports/?spec_id=${encodeURIComponent(id)}`);
      if (res.ok) {
        const data = await res.json();
        setRunHistory(data);
      }
    } catch (e) {
      console.error("Failed to load contract history:", e);
    }
  }, [apiFetch]);

  // Connection check loop
  useEffect(() => {
    const ping = async () => {
      try {
        const res = await fetch(`${serverUrl}/health`);
        const data = await res.json();
        setConnected(data.status === 'ok');
      } catch (err) {
        console.error("Ping failed:", err);
        setConnected(false);
      }
    };
    ping();
    const interval = setInterval(ping, 5000);
    return () => clearInterval(interval);
  }, [serverUrl]);

  // Load Specs & Running Mocks list when connected
  useEffect(() => {
    if (connected) {
      const t = setTimeout(() => {
        loadSpecs();
        loadRunningMocks();
      }, 0);
      return () => clearTimeout(t);
    }
  }, [connected, loadSpecs, loadRunningMocks]);

  // Load selected spec details
  useEffect(() => {
    if (connected && selectedSpecId) {
      const t = setTimeout(() => {
        loadSpecDetails(selectedSpecId);
        loadMockConfig(selectedSpecId);
        loadContractHistory(selectedSpecId);
      }, 0);
      return () => clearTimeout(t);
    } else {
      const t = setTimeout(() => {
        setSelectedSpec(prev => {
          if (prev !== null) return null;
          return prev;
        });
      }, 0);
      return () => clearTimeout(t);
    }
  }, [selectedSpecId, connected, loadSpecDetails, loadMockConfig, loadContractHistory]);

  // Upload spec handler
  const handleUploadSpec = async (e) => {
    e.preventDefault();
    if (!newSpecId || !newSpecRaw) return alert('Please enter both specification ID and YAML/JSON content');

    setUploading(true);
    try {
      const res = await apiFetch('/api/specs', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: newSpecId, raw: newSpecRaw })
      });
      if (res.ok) {
        alert('Specification loaded and registered successfully!');
        setNewSpecId('');
        setNewSpecRaw('');
        loadSpecs();
      } else {
        const err = await res.json();
        alert(`Failed to load spec: ${err.error}`);
      }
    } catch (e) {
      alert(`Error uploading spec: ${e.message}`);
    } finally {
      setUploading(false);
    }
  };

  // Update Mock Config state locally
  const handleConfigChange = (specId, field, val, isChaos = false) => {
    setMockConfigs(prev => {
      const existing = prev[specId] || {
        host: '127.0.0.1',
        port: 0,
        chaos: {
          latency_ms: 0,
          latency_jitter_ms: 0,
          error_rate: 0,
          error_status: 500,
          drop_connection_rate: 0
        }
      };

      if (isChaos) {
        return {
          ...prev,
          [specId]: {
            ...existing,
            chaos: {
              ...existing.chaos,
              [field]: val
            }
          }
        };
      }

      return {
        ...prev,
        [specId]: {
          ...existing,
          [field]: val
        }
      };
    });
  };

  // Save config request
  const saveMockConfig = async (specId) => {
    const config = mockConfigs[specId];
    if (!config) return;

    setSavingConfigSpecId(specId);
    try {
      const res = await apiFetch('/api/mocks/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: specId, config })
      });
      if (res.ok) {
        alert('Mock configuration saved successfully!');
      } else {
        const err = await res.json();
        alert(`Failed to save config: ${err.error}`);
      }
    } catch (e) {
      alert(`Error saving config: ${e.message}`);
    } finally {
      setSavingConfigSpecId(null);
    }
  };

  // Start mock server
  const handleStartMock = async (specId) => {
    // Save first to make sure server uses latest configs
    await saveMockConfig(specId);

    try {
      const res = await apiFetch('/api/mocks/start', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: specId })
      });
      if (res.ok) {
        const data = await res.json();
        setRunningMocks(prev => ({ ...prev, [specId]: data.address }));
        loadRunningMocks();
      } else {
        const err = await res.json();
        alert(`Failed to start mock server: ${err.error}`);
      }
    } catch (e) {
      alert(`Error starting mock server: ${e.message}`);
    }
  };

  // Stop mock server
  const handleStopMock = async (specId) => {
    try {
      const res = await apiFetch('/api/mocks/stop', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: specId })
      });
      if (res.ok) {
        setRunningMocks(prev => {
          const updated = { ...prev };
          delete updated[specId];
          return updated;
        });
        loadRunningMocks();
      } else {
        const err = await res.json();
        alert(`Failed to stop mock server: ${err.error}`);
      }
    } catch (e) {
      alert(`Error stopping mock server: ${e.message}`);
    }
  };

  // Run SUT Contract Checks
  const handleRunContract = async (e) => {
    e.preventDefault();
    if (!selectedSpecId) return alert('Select a specification first.');
    if (!targetUrl) return alert('Specify target System Under Test (SUT) URL.');

    setRunningCheck(true);
    setCheckResult(null);
    try {
      const res = await apiFetch('/api/contract/run', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: selectedSpecId, target_url: targetUrl })
      });
      if (res.ok) {
        const data = await res.json();
        setCheckResult(data);
        loadContractHistory(selectedSpecId);
      } else {
        const err = await res.json();
        alert(`Contract runner failed: ${err.error}`);
      }
    } catch (e) {
      alert(`Error running contract checks: ${e.message}`);
    } finally {
      setRunningCheck(false);
    }
  };

  // View historical drift details
  const viewHistoryDetail = async (runId) => {
    try {
      const res = await apiFetch(`/api/reports/${runId}`);
      if (res.ok) {
        const data = await res.json();
        setSelectedHistoryRun({ runId, findings: data.findings || [] });
      }
    } catch (e) {
      alert(`Failed to load historical report: ${e.message}`);
    }
  };

  const getActiveConfig = (specId) => {
    return mockConfigs[specId] || {
      host: '127.0.0.1',
      port: 0,
      chaos: {
        latency_ms: 0,
        latency_jitter_ms: 0,
        error_rate: 0,
        error_status: 500,
        drop_connection_rate: 0
      }
    };
  };

  return (
    <div className="app-container">
      {/* Sidebar navigation */}
      <div className="sidebar">
        <div className="brand">
          <div className="brand-icon">
            <Activity size={20} />
          </div>
          <span className="brand-name">SPECGUARD</span>
        </div>

        <div className="nav-links">
          <div 
            className={`nav-item ${activeTab === 'specs' ? 'active' : ''}`}
            onClick={() => setActiveTab('specs')}
          >
            <Database className="nav-icon" />
            <span>Specifications</span>
          </div>

          <div 
            className={`nav-item ${activeTab === 'mocks' ? 'active' : ''}`}
            onClick={() => setActiveTab('mocks')}
          >
            <Cpu className="nav-icon" />
            <span>Mocks Manager</span>
          </div>

          <div 
            className={`nav-item ${activeTab === 'contracts' ? 'active' : ''}`}
            onClick={() => setActiveTab('contracts')}
          >
            <ShieldCheck className="nav-icon" />
            <span>Contract Runner</span>
          </div>

          <div 
            className={`nav-item ${activeTab === 'history' ? 'active' : ''}`}
            onClick={() => {
              setActiveTab('history');
              setSelectedHistoryRun(null);
            }}
          >
            <History className="nav-icon" />
            <span>Runs History</span>
          </div>
        </div>

        {/* Server connection setup in sidebar footer */}
        <div className="server-config">
          <div className="config-title">Connection Settings</div>
          <div className="config-input-wrapper">
            <ServerIcon size={14} style={{ color: 'var(--text-secondary)', marginRight: '6px' }} />
            <input 
              type="text" 
              className="config-input" 
              value={serverUrl} 
              onChange={(e) => setServerUrl(e.target.value)} 
              placeholder="http://localhost:8080"
            />
          </div>
          <div className="config-title">API Key</div>
          <div className="config-input-wrapper">
            <ShieldCheck size={14} style={{ color: 'var(--text-secondary)', marginRight: '6px' }} />
            <input 
              type="password" 
              className="config-input" 
              value={apiKey} 
              onChange={(e) => setApiKey(e.target.value)} 
              placeholder="Enter API Key"
            />
          </div>
          <div className="connection-status">
            <div className={`status-indicator ${connected ? 'status-online' : 'status-offline'}`} />
            <span style={{ color: connected ? 'var(--text-primary)' : 'var(--status-error)' }}>
              {connected ? 'Server Connected' : 'Server Disconnected'}
            </span>
          </div>
        </div>
      </div>

      {/* Main workspace */}
      <div className="main-content">
        
        {/* TAB 1: Specifications */}
        {activeTab === 'specs' && (
          <div>
            <div className="header">
              <div>
                <h1>Specifications Browser</h1>
                <p className="title-desc">Add, inspect, and analyze registered protocol specifications.</p>
              </div>
            </div>

            <div className="layout-split">
              {/* Left Column: Spec list and Upload */}
              <div>
                <div className="card">
                  <div className="card-title">
                    <Database size={18} style={{ color: 'var(--accent-primary)' }} />
                    <span>Registered Specs</span>
                  </div>
                  <div className="list-container">
                    {specs.length === 0 ? (
                      <div style={{ color: 'var(--text-muted)', fontSize: '13px', textAlign: 'center', padding: '10px 0' }}>
                        No specifications uploaded yet.
                      </div>
                    ) : (
                      specs.map(id => (
                        <div 
                          key={id} 
                          className={`list-item ${selectedSpecId === id ? 'selected' : ''}`}
                          onClick={() => setSelectedSpecId(id)}
                        >
                          <span className="list-item-title">{id}</span>
                          {runningMocks[id] && (
                            <span className="connection-status" style={{ fontSize: '11px' }}>
                              <span className="status-indicator status-online" style={{ width: '6px', height: '6px' }} />
                              <span>Live</span>
                            </span>
                          )}
                        </div>
                      ))
                    )}
                  </div>
                </div>

                <div className="card">
                  <div className="card-title">
                    <Upload size={18} style={{ color: 'var(--accent-purple)' }} />
                    <span>Register New Spec</span>
                  </div>
                  <form onSubmit={handleUploadSpec}>
                    <div className="form-group">
                      <label className="form-label">Specification ID</label>
                      <input 
                        type="text" 
                        className="form-input" 
                        placeholder="e.g. petstore-api"
                        value={newSpecId}
                        onChange={(e) => setNewSpecId(e.target.value)}
                      />
                    </div>
                    <div className="form-group">
                      <label className="form-label">OpenAPI Specs Content (YAML / JSON)</label>
                      <textarea 
                        className="form-input" 
                        rows="8" 
                        placeholder="Paste raw OpenAPI definition here..."
                        style={{ fontFamily: 'var(--font-mono)', fontSize: '12px', resize: 'vertical' }}
                        value={newSpecRaw}
                        onChange={(e) => setNewSpecRaw(e.target.value)}
                      />
                    </div>
                    <button type="submit" className="btn btn-primary" style={{ width: '100%' }} disabled={uploading}>
                      {uploading ? (
                        <>
                          <div className="spinner" />
                          <span>Registering...</span>
                        </>
                      ) : (
                        <>
                          <Plus size={16} />
                          <span>Register Specification</span>
                        </>
                      )}
                    </button>
                  </form>
                </div>
              </div>

              {/* Right Column: Spec Operations details */}
              <div>
                {selectedSpec ? (
                  <div className="card" style={{ minHeight: '500px' }}>
                    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
                      <div>
                        <h2 style={{ fontSize: '22px' }}>{selectedSpecId}</h2>
                        <span style={{ fontSize: '12px', color: 'var(--text-secondary)', fontFamily: 'var(--font-mono)' }}>
                          Operations Total: {Object.keys(selectedSpec.Operations || {}).length}
                        </span>
                      </div>
                      <div className="btn btn-secondary btn-sm" onClick={() => loadSpecDetails(selectedSpecId)}>
                        <RefreshCw size={12} />
                        <span>Reload</span>
                      </div>
                    </div>

                    <div className="op-card-grid">
                      {Object.keys(selectedSpec.Operations || {}).length === 0 ? (
                        <div className="detail-empty">
                          <FileCode className="empty-icon" />
                          <p>No operations found in this specification structure.</p>
                        </div>
                      ) : (
                        Object.entries(selectedSpec.Operations).map(([opId, op]) => (
                          <OperationAccordion key={opId} opId={opId} op={op} />
                        ))
                      )}
                    </div>
                  </div>
                ) : (
                  <div className="card" style={{ minHeight: '500px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                    <div className="detail-empty">
                      <Database className="empty-icon" />
                      <p>Select a specification from the list to explore its architecture.</p>
                    </div>
                  </div>
                )}
              </div>
            </div>
          </div>
        )}

        {/* TAB 2: Mocks Management */}
        {activeTab === 'mocks' && (
          <div>
            <div className="header">
              <div>
                <h1>Mocks & Chaos Engine</h1>
                <p className="title-desc">Spin up plugin-driven mock servers and configure real-time error injection.</p>
              </div>
            </div>

            <div className="layout-split">
              {/* Left Column: Spec selector */}
              <div>
                <div className="card">
                  <div className="card-title">
                    <Database size={18} style={{ color: 'var(--accent-primary)' }} />
                    <span>Select Target Spec</span>
                  </div>
                  <div className="list-container">
                    {specs.length === 0 ? (
                      <div style={{ color: 'var(--text-muted)', fontSize: '13px', textAlign: 'center', padding: '10px 0' }}>
                        No specifications uploaded yet.
                      </div>
                    ) : (
                      specs.map(id => (
                        <div 
                          key={id} 
                          className={`list-item ${selectedSpecId === id ? 'selected' : ''}`}
                          onClick={() => setSelectedSpecId(id)}
                        >
                          <span className="list-item-title">{id}</span>
                          {runningMocks[id] && (
                            <span className="connection-status" style={{ fontSize: '11px' }}>
                              <span className="status-indicator status-online" style={{ width: '6px', height: '6px' }} />
                              <span>Live</span>
                            </span>
                          )}
                        </div>
                      ))
                    )}
                  </div>
                </div>
              </div>

              {/* Right Column: Server status, Mock address and Chaos Sliders */}
              <div>
                {selectedSpecId ? (
                  <div className="card">
                    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px', borderBottom: '1px solid var(--border-color)', paddingBottom: '16px' }}>
                      <div>
                        <h2>Mock Server: {selectedSpecId}</h2>
                        {runningMocks[selectedSpecId] ? (
                          <div className="connection-status" style={{ marginTop: '6px' }}>
                            <div className="status-indicator status-online" />
                            <span style={{ fontFamily: 'var(--font-mono)', fontSize: '13px', fontWeight: 600 }}>
                              Running at {runningMocks[selectedSpecId]}
                            </span>
                          </div>
                        ) : (
                          <div className="connection-status" style={{ marginTop: '6px' }}>
                            <div className="status-indicator status-offline" />
                            <span style={{ color: 'var(--text-secondary)', fontSize: '13px' }}>Stopped</span>
                          </div>
                        )}
                      </div>

                      <div style={{ display: 'flex', gap: '10px' }}>
                        {runningMocks[selectedSpecId] ? (
                          <button className="btn btn-danger" onClick={() => handleStopMock(selectedSpecId)}>
                            <Square size={14} />
                            <span>Stop Mock</span>
                          </button>
                        ) : (
                          <button className="btn btn-primary" onClick={() => handleStartMock(selectedSpecId)}>
                            <Play size={14} />
                            <span>Start Mock</span>
                          </button>
                        )}
                      </div>
                    </div>

                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '30px' }}>
                      {/* Host & Port setup */}
                      <div>
                        <h3 style={{ fontSize: '16px', marginBottom: '16px', display: 'flex', alignItems: 'center', gap: '8px', color: 'var(--text-primary)' }}>
                          <ServerIcon size={16} />
                          <span>Mock Host Config</span>
                        </h3>
                        <div className="form-group">
                          <label className="form-label">Host Address</label>
                          <input 
                            type="text" 
                            className="form-input" 
                            value={getActiveConfig(selectedSpecId).host}
                            onChange={(e) => handleConfigChange(selectedSpecId, 'host', e.target.value)}
                            placeholder="127.0.0.1"
                          />
                        </div>
                        <div className="form-group">
                          <label className="form-label">Port (0 for dynamic assignment)</label>
                          <input 
                            type="number" 
                            className="form-input" 
                            value={getActiveConfig(selectedSpecId).port}
                            onChange={(e) => handleConfigChange(selectedSpecId, 'port', parseInt(e.target.value) || 0)}
                            placeholder="0"
                          />
                        </div>

                        <button 
                          className="btn btn-secondary" 
                          style={{ marginTop: '16px', width: '100%' }}
                          onClick={() => saveMockConfig(selectedSpecId)}
                          disabled={savingConfigSpecId === selectedSpecId}
                        >
                          {savingConfigSpecId === selectedSpecId ? 'Saving...' : 'Save Configuration'}
                        </button>
                      </div>

                      {/* Chaos Settings */}
                      <div>
                        <h3 style={{ fontSize: '16px', marginBottom: '16px', display: 'flex', alignItems: 'center', gap: '8px', color: 'var(--accent-purple)' }}>
                          <Sliders size={16} />
                          <span>Chaos Injection Settings</span>
                        </h3>

                        <div className="chaos-slider-container">
                          <div className="slider-info">
                            <span className="slider-label">Latency Delay</span>
                            <span className="slider-value">{getActiveConfig(selectedSpecId).chaos.latency_ms} ms</span>
                          </div>
                          <input 
                            type="range" 
                            min="0" 
                            max="2000" 
                            step="50"
                            value={getActiveConfig(selectedSpecId).chaos.latency_ms}
                            onChange={(e) => handleConfigChange(selectedSpecId, 'latency_ms', parseInt(e.target.value), true)}
                          />
                        </div>

                        <div className="chaos-slider-container">
                          <div className="slider-info">
                            <span className="slider-label">Latency Jitter</span>
                            <span className="slider-value">{getActiveConfig(selectedSpecId).chaos.latency_jitter_ms} ms</span>
                          </div>
                          <input 
                            type="range" 
                            min="0" 
                            max="1000" 
                            step="25"
                            value={getActiveConfig(selectedSpecId).chaos.latency_jitter_ms}
                            onChange={(e) => handleConfigChange(selectedSpecId, 'latency_jitter_ms', parseInt(e.target.value), true)}
                          />
                        </div>

                        <div className="chaos-slider-container">
                          <div className="slider-info">
                            <span className="slider-label">Error Inject Rate</span>
                            <span className="slider-value">{(getActiveConfig(selectedSpecId).chaos.error_rate * 100).toFixed(0)}%</span>
                          </div>
                          <input 
                            type="range" 
                            min="0" 
                            max="1" 
                            step="0.05"
                            value={getActiveConfig(selectedSpecId).chaos.error_rate}
                            onChange={(e) => handleConfigChange(selectedSpecId, 'error_rate', parseFloat(e.target.value), true)}
                          />
                        </div>

                        <div className="form-group" style={{ marginTop: '6px' }}>
                          <label className="form-label">Error Status Code</label>
                          <input 
                            type="number" 
                            className="form-input" 
                            value={getActiveConfig(selectedSpecId).chaos.error_status}
                            onChange={(e) => handleConfigChange(selectedSpecId, 'error_status', parseInt(e.target.value) || 500, true)}
                            placeholder="500"
                          />
                        </div>

                        <div className="chaos-slider-container">
                          <div className="slider-info">
                            <span className="slider-label">Drop Connection Rate</span>
                            <span className="slider-value">{(getActiveConfig(selectedSpecId).chaos.drop_connection_rate * 100).toFixed(0)}%</span>
                          </div>
                          <input 
                            type="range" 
                            min="0" 
                            max="1" 
                            step="0.05"
                            value={getActiveConfig(selectedSpecId).chaos.drop_connection_rate}
                            onChange={(e) => handleConfigChange(selectedSpecId, 'drop_connection_rate', parseFloat(e.target.value), true)}
                          />
                        </div>
                      </div>
                    </div>
                  </div>
                ) : (
                  <div className="card" style={{ minHeight: '400px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                    <div className="detail-empty">
                      <Cpu className="empty-icon" />
                      <p>Select a specification to manage mock servers and chaos variables.</p>
                    </div>
                  </div>
                )}
              </div>
            </div>
          </div>
        )}

        {/* TAB 3: Contract Runner */}
        {activeTab === 'contracts' && (
          <div>
            <div className="header">
              <div>
                <h1>Contract Testing Runner</h1>
                <p className="title-desc">Ping a target System Under Test (SUT) to detect deviations from OpenAPI declarations.</p>
              </div>
            </div>

            <div className="layout-split">
              {/* Left Column: Spec selection */}
              <div>
                <div className="card">
                  <div className="card-title">
                    <Database size={18} style={{ color: 'var(--accent-primary)' }} />
                    <span>Selected Contract Spec</span>
                  </div>
                  <div className="list-container">
                    {specs.length === 0 ? (
                      <div style={{ color: 'var(--text-muted)', fontSize: '13px', textAlign: 'center', padding: '10px 0' }}>
                        No specifications uploaded yet.
                      </div>
                    ) : (
                      specs.map(id => (
                        <div 
                          key={id} 
                          className={`list-item ${selectedSpecId === id ? 'selected' : ''}`}
                          onClick={() => setSelectedSpecId(id)}
                        >
                          <span className="list-item-title">{id}</span>
                        </div>
                      ))
                    )}
                  </div>
                </div>

                {selectedSpecId && (
                  <div className="card">
                    <div className="card-title">
                      <ShieldCheck size={18} style={{ color: 'var(--accent-purple)' }} />
                      <span>Trigger Runner</span>
                    </div>
                    <form onSubmit={handleRunContract}>
                      <div className="form-group">
                        <label className="form-label">SUT Target Host URL</label>
                        <input 
                          type="text" 
                          className="form-input" 
                          placeholder="http://localhost:9000"
                          value={targetUrl}
                          onChange={(e) => setTargetUrl(e.target.value)}
                        />
                        {selectedSpecId && runningMocks[selectedSpecId] && (
                          <div 
                            style={{ fontSize: '11px', color: 'var(--accent-primary)', cursor: 'pointer', marginTop: '4px' }}
                            onClick={() => setTargetUrl(runningMocks[selectedSpecId])}
                          >
                            Use active Mock Server URL ({runningMocks[selectedSpecId]})
                          </div>
                        )}
                      </div>
                      <button type="submit" className="btn btn-primary" style={{ width: '100%' }} disabled={runningCheck}>
                        {runningCheck ? (
                          <>
                            <div className="spinner" />
                            <span>Running checks...</span>
                          </>
                        ) : (
                          <>
                            <Play size={16} />
                            <span>Execute Checks</span>
                          </>
                        )}
                      </button>
                    </form>
                  </div>
                )}
              </div>

              {/* Right Column: Drift results */}
              <div>
                {checkResult ? (
                  <div className="card">
                    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', borderBottom: '1px solid var(--border-color)', paddingBottom: '16px', marginBottom: '20px' }}>
                      <div>
                        <h2>Check Result</h2>
                        <span style={{ fontSize: '12px', color: 'var(--text-secondary)', fontFamily: 'var(--font-mono)' }}>
                          Run ID: {checkResult.run_id}
                        </span>
                      </div>
                      {checkResult.passed ? (
                        <div className="btn btn-secondary" style={{ backgroundColor: 'var(--status-success-bg)', color: '#34d399', cursor: 'default' }}>
                          <CheckCircle2 size={16} />
                          <span>Conformant</span>
                        </div>
                      ) : (
                        <div className="btn btn-secondary" style={{ backgroundColor: 'var(--status-error-bg)', color: '#f87171', cursor: 'default' }}>
                          <AlertTriangle size={16} />
                          <span>Drifts Detected</span>
                        </div>
                      )}
                    </div>

                    <div>
                      <h3 style={{ fontSize: '16px', marginBottom: '14px' }}>Findings ({checkResult.drift_report?.findings?.length || 0})</h3>
                      {(!checkResult.drift_report?.findings || checkResult.drift_report.findings.length === 0) ? (
                        <div style={{ padding: '40px', textAlign: 'center', color: 'var(--text-secondary)' }}>
                          <CheckCircle2 size={32} style={{ color: 'var(--status-success)', marginBottom: '8px' }} />
                          <p>All endpoints conform perfectly to the specifications declaration!</p>
                        </div>
                      ) : (
                        <div className="findings-list">
                          {checkResult.drift_report.findings.map((f, i) => (
                            <div key={i} className={`finding-item ${f.severity === 'error' ? 'finding-error' : 'finding-warning'}`}>
                              <div className="finding-header">
                                <span style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                                  {f.severity === 'error' ? <AlertCircle size={14} /> : <AlertTriangle size={14} />}
                                  <span>{f.kind}</span>
                                </span>
                                <span>{f.severity}</span>
                              </div>
                              <div className="finding-loc">Location: {f.location}</div>
                              <div className="finding-diff">
                                <div className="finding-expected">- Expected: {f.expected}</div>
                                <div className="finding-actual">+ Actual: {f.actual}</div>
                              </div>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  </div>
                ) : (
                  <div className="card" style={{ minHeight: '400px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                    <div className="detail-empty">
                      <ShieldCheck className="empty-icon" />
                      <p>Run contract tests on a target server host to review drift findings.</p>
                    </div>
                  </div>
                )}
              </div>
            </div>
          </div>
        )}

        {/* TAB 4: Runs History */}
        {activeTab === 'history' && (
          <div>
            <div className="header">
              <div>
                <h1>Runs History</h1>
                <p className="title-desc">Inspect historical contract validation runs and logs.</p>
              </div>
            </div>

            <div className="layout-split">
              {/* Left Column: Spec selection and history log */}
              <div>
                <div className="card">
                  <div className="card-title">
                    <Database size={18} style={{ color: 'var(--accent-primary)' }} />
                    <span>Select Spec</span>
                  </div>
                  <div className="list-container">
                    {specs.length === 0 ? (
                      <div style={{ color: 'var(--text-muted)', fontSize: '13px', textAlign: 'center', padding: '10px 0' }}>
                        No specifications uploaded yet.
                      </div>
                    ) : (
                      specs.map(id => (
                        <div 
                          key={id} 
                          className={`list-item ${selectedSpecId === id ? 'selected' : ''}`}
                          onClick={() => setSelectedSpecId(id)}
                        >
                          <span className="list-item-title">{id}</span>
                        </div>
                      ))
                    )}
                  </div>
                </div>

                {selectedSpecId && (
                  <div className="card">
                    <div className="card-title">
                      <History size={18} style={{ color: 'var(--accent-purple)' }} />
                      <span>Past Runs</span>
                    </div>
                    <div className="list-container" style={{ maxHeight: '350px' }}>
                      {runHistory.length === 0 ? (
                        <div style={{ color: 'var(--text-muted)', fontSize: '13px', textAlign: 'center', padding: '10px 0' }}>
                          No contract test runs recorded.
                        </div>
                      ) : (
                        runHistory.map(run => (
                          <div 
                            key={run.id}
                            className={`list-item ${selectedHistoryRun?.runId === run.id ? 'selected' : ''}`}
                            onClick={() => viewHistoryDetail(run.id)}
                            style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: '4px' }}
                          >
                            <div style={{ display: 'flex', justifyContent: 'space-between', width: '100%' }}>
                              <span style={{ fontSize: '11px', fontFamily: 'var(--font-mono)', color: 'var(--text-muted)' }}>{run.id}</span>
                              <span 
                                style={{ 
                                  fontSize: '10px', 
                                  fontWeight: 'bold',
                                  color: run.passed ? 'var(--status-success)' : 'var(--status-error)' 
                                }}
                              >
                                {run.passed ? 'PASSED' : 'FAILED'}
                              </span>
                            </div>
                            <div style={{ fontSize: '12px', color: 'var(--text-secondary)' }}>Target: {run.target_url}</div>
                            <div style={{ fontSize: '10px', color: 'var(--text-muted)' }}>{new Date(run.created_at).toLocaleString()}</div>
                          </div>
                        ))
                      )}
                    </div>
                  </div>
                )}
              </div>

              {/* Right Column: History report details */}
              <div>
                {selectedHistoryRun ? (
                  <div className="card">
                    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', borderBottom: '1px solid var(--border-color)', paddingBottom: '16px', marginBottom: '20px' }}>
                      <div>
                        <h2>Drift Report</h2>
                        <span style={{ fontSize: '12px', color: 'var(--text-secondary)', fontFamily: 'var(--font-mono)' }}>
                          Run ID: {selectedHistoryRun.runId}
                        </span>
                      </div>
                    </div>

                    <div>
                      <h3 style={{ fontSize: '16px', marginBottom: '14px' }}>Findings ({selectedHistoryRun.findings.length})</h3>
                      {selectedHistoryRun.findings.length === 0 ? (
                        <div style={{ padding: '40px', textAlign: 'center', color: 'var(--text-secondary)' }}>
                          <CheckCircle2 size={32} style={{ color: 'var(--status-success)', marginBottom: '8px' }} />
                          <p>This run had zero drift findings. Completely conformant!</p>
                        </div>
                      ) : (
                        <div className="findings-list">
                          {selectedHistoryRun.findings.map((f, i) => (
                            <div key={i} className={`finding-item ${f.severity === 'error' ? 'finding-error' : 'finding-warning'}`}>
                              <div className="finding-header">
                                <span style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                                  {f.severity === 'error' ? <AlertCircle size={14} /> : <AlertTriangle size={14} />}
                                  <span>{f.kind}</span>
                                </span>
                                <span>{f.severity}</span>
                              </div>
                              <div className="finding-loc">Location: {f.location}</div>
                              <div className="finding-diff">
                                <div className="finding-expected">- Expected: {f.expected}</div>
                                <div className="finding-actual">+ Actual: {f.actual}</div>
                              </div>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  </div>
                ) : (
                  <div className="card" style={{ minHeight: '400px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                    <div className="detail-empty">
                      <History className="empty-icon" />
                      <p>Select a specific past run log to check its drift details.</p>
                    </div>
                  </div>
                )}
              </div>
            </div>
          </div>
        )}

      </div>
    </div>
  );
}
