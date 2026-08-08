"""加载并导出内嵌的 HTML 模板。

模板文件（``templates/bootstrap.html`` 与 ``templates/admin.html``）在模块
导入时一次性读入内存并缓存为模块级常量，避免每个请求重复读盘。模板路径
固定基于本文件所在目录，与运行时配置目录（``state.config_dir``）无关。
"""

import os

template_dir: str = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'templates')


def load_template(name: str) -> str:
    """读取指定模板文件的完整内容（UTF-8）。"""

    with open(os.path.join(template_dir, name), 'r', encoding='utf-8') as f:
        return f.read()


BOOTSTRAP_HTML: str = load_template('bootstrap.html')
ADMIN_HTML: str = load_template('admin.html')
