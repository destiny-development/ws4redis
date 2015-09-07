from fabric.api import *
from fabric.colors import *
from fabric.contrib.console import confirm

env.user = 'tera'
env.use_ssh_config = True

env.base_dir = '/home/tera/ws4redis/'

def dev():
    env.hosts = ['dev.tera-online.ru']


def live():
    env.hosts = ['tera-online.ru', ]

    if not confirm(red("Are you going to deploy on LIVE?"), default=False):
        abort('Aborted by request.')

def build():
    local('go test .')
    local('go build .')

def deploy(branch='master', restart='yes', skip_build='yes'):
    if skip_build != 'yes':
        build()
    with cd(env.base_dir):
        put('ws4redis', 'ws4redis.new')
        run('chmod +x ws4redis.new')
        run('mv ws4redis.new ws4redis')
        if restart == 'yes':
            run('killall ws4redis')
