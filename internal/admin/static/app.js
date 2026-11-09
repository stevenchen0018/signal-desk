const channelType = document.querySelector("#type, #trigger, .notification-test-form #channel_id");
if (channelType) {
  const fields = Array.from(document.querySelectorAll("[data-for]"));
  const update = () => {
    for (const field of fields) {
      const visible = field.dataset.for.split(" ").includes(channelType.value);
      field.hidden = !visible;
      for (const input of field.querySelectorAll("input, select, textarea")) input.disabled = !visible;
    }
  };
  channelType.addEventListener("change", update);
  update();
}

for (const button of document.querySelectorAll("[data-confirm]")) {
  button.addEventListener("click", (event) => {
    if (!window.confirm(button.dataset.confirm)) event.preventDefault();
  });
}

if (document.querySelector("[data-model-query-pending]")) {
  window.setTimeout(() => window.location.reload(), 1800);
}

const insertTable = document.querySelector("#insert-template-table");
if (insertTable) {
  const format = document.querySelector("#format");
  const body = document.querySelector("#body");
  const tableExample = [
    '<table border="1" cellpadding="6" cellspacing="0">',
    '  {{if .MailModels}}',
    '  <thead><tr><th>模型</th><th>下线时间</th><th>推荐替代模型</th></tr></thead>',
    '  <tbody>{{range .MailModels}}',
    '    <tr><td>{{.ModelName}}</td><td>{{.OfflineTime}}</td><td>{{.ReplacementModel}}</td></tr>',
    '  {{end}}</tbody>',
    '  {{else}}',
    '  <thead><tr><th>模型</th><th>事件</th><th>时间</th></tr></thead>',
    '  <tbody>{{if .Models}}{{range .Models}}',
    '    <tr><td>{{.}}</td><td>{{$.Event}}</td><td>{{$.Time}}</td></tr>',
    '    {{end}}{{else}}',
    '    <tr><td>{{if .Model}}{{.Model}}{{else}}{{.Subject}}{{end}}</td><td>{{.Event}}</td><td>{{.Time}}</td></tr>',
    '    {{end}}',
    '  </tbody>{{end}}',
    '</table>',
  ].join("\n");
  const showTableButton = () => { insertTable.hidden = format.value !== "html"; };
  format.addEventListener("change", showTableButton);
  showTableButton();
  insertTable.addEventListener("click", () => {
    body.setRangeText("\n" + tableExample + "\n", body.selectionStart, body.selectionEnd, "end");
    body.focus();
  });
}
