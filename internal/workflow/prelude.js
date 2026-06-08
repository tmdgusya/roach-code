// Dynamic-workflow prelude. The model's script runs after this with the globals
// below available. There is no DAG to declare — control flow IS the dependency
// graph: `await` sequences dependent steps, parallel()/Promise.all fan out.
//
//   agent(prompt, opts?) -> Promise<string>   spawn one sub-agent; await its answer
//   parallel(thunks)     -> Promise<any[]>    run thunks concurrently (barrier)
//   pipeline(items, ...stages) -> Promise<any[]>  each item flows through all stages
//   log(msg) / phase(title)                   progress lines for the user
//   args                                       the JSON value passed to run_workflow
//   budget   -> {total, spent, remaining, agents, maxAgents}  live caps snapshot
//
// opts: { label?: string, tools?: string[] }. Only agent()'s return value chain
// reaches the model — intermediates stay in these script variables.

globalThis.agent = function (prompt, opts) {
  opts = opts || {};
  return __agent(JSON.stringify({ prompt: prompt, label: opts.label, tools: opts.tools }));
};

globalThis.log = function (m) { __log(String(m)); };
globalThis.phase = function (t) { __phase(String(t)); };

Object.defineProperty(globalThis, 'budget', {
  get: function () { return __budget(); },
});

// parallel(thunks): run all thunks concurrently and wait for all (a barrier). A
// thunk that throws resolves to null — filter(Boolean) before use.
globalThis.parallel = function (thunks) {
  return Promise.all(thunks.map(function (t) {
    return Promise.resolve().then(t).catch(function () { return null; });
  }));
};

// pipeline(items, ...stages): each item flows through every stage independently
// (no barrier between stages). A stage that throws drops that item to null.
// Each stage receives (prevResult, originalItem, index).
globalThis.pipeline = function (items) {
  var stages = Array.prototype.slice.call(arguments, 1);
  return Promise.all(items.map(async function (item, i) {
    var v = item;
    for (var s = 0; s < stages.length; s++) {
      try {
        v = await stages[s](v, item, i);
      } catch (e) {
        return null;
      }
    }
    return v;
  }));
};
