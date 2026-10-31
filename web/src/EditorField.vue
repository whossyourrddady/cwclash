<script setup lang="ts">
import type {Field, Entry} from './proxy-editor'
import {getField,setField} from './proxy-editor'
const props=defineProps<{field:Field; value:Entry; reveal?:boolean}>()
function change(event:Event){const input=event.target as HTMLInputElement;setField(props.value,props.field.key,input.value===''?undefined:props.field.type==='number'?Number(input.value):input.value)}
</script>
<template><label>{{ field.label }}<select v-if="field.type==='select'" :aria-label="field.label" :value="getField(value,field.key)" @change="change"><option value="">沿用默认 / 不设置</option><option v-for="item in [...new Set([...(field.choices||[]),getField(value,field.key)].filter(Boolean))]" :key="item" :value="item">{{ item }}</option></select><input v-else :type="field.type==='password' ? (reveal?'text':'password') : field.type||'text'" :value="getField(value,field.key)" :list="field.choices?'visual-'+field.key:undefined" autocomplete="off" spellcheck="false" @input="change" /><datalist v-if="field.choices && field.type!=='select'" :id="'visual-'+field.key"><option v-for="item in field.choices" :key="item" :value="item" /></datalist></label></template>
